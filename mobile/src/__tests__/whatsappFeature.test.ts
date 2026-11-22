/* eslint-disable import/first -- the mocks must come before the modules they replace */
// WhatsApp alerts and the hub key (issue #49): the app reads who receives
// the household's alerts and which provider carries them, asks the hub for
// the wa.me link, and the owner's "Répondre 1 pour bloquer" switch signs
// the hub key's hub_block delegation into the bundle — or revokes it.
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../licensing/household', () => ({
  withHousehold: (run: (tok: string, id: string) => Promise<unknown>) => run('tok', 'T1'),
}));
const mockHub = {
  whatsapp: jest.fn(),
  whatsappOptIn: jest.fn(),
  hubKey: jest.fn(),
  members: jest.fn(),
  trustBundle: jest.fn(),
  putTrustBundle: jest.fn(),
};
jest.mock('../licensing/hub', () => ({
  get hub() {
    return mockHub;
  },
}));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test' } }));

import KerguiCore from '../../modules/kergui-core';
import { loadAlertChannels, useDemoWhatsApp, whatsapp, DEMO_PROVIDER } from '../features/whatsapp';

const core = KerguiCore as unknown as Record<'remoteRelays' | 'signTrustBundle', jest.Mock>;
const BUNDLE = { payload: 'UA==', sig: 'Uw==', version: 3 };
const NEXT = { payload: 'Tg==', sig: 'Tg==', version: 4 };
const KEY = { enabled: true, keyId: 'k-hub', scope: 'hub_block', ed25519: 'SFVC', x25519: 'WA==' };
const EVOLUTION = { name: 'evolution', notice: 'Messagerie WhatsApp assurée par Evolution Go (evolution-api.com).' };
const you = (role: string) => ({ membershipId: 'M0', userId: 'U0', role });
const ownerView = (over: object = {}) => JSON.stringify({ owner: true, ownerOnly: false, relays: [{ agentId: 'A1', kind: 'pc' }], delegations: [], ...over });

beforeEach(() => {
  for (const m of Object.values(mockHub)) m.mockReset();
  core.remoteRelays.mockReset().mockResolvedValue(ownerView());
  core.signTrustBundle.mockReset().mockResolvedValue(JSON.stringify(NEXT));
  mockHub.whatsapp.mockResolvedValue({ provider: EVOLUTION, optIns: [{ userId: 'U0', phone: '+221••••67', active: true, linkedAt: 'x' }] });
  mockHub.members.mockResolvedValue({ household: { id: 'T1', name: 'Foyer' }, you: you('owner'), members: [] });
  mockHub.hubKey.mockResolvedValue(KEY);
  mockHub.trustBundle.mockResolvedValue(BUNDLE);
  mockHub.putTrustBundle.mockResolvedValue(undefined);
});

test('the owner sees the provider notice, that their number is linked, and the switch off', async () => {
  const v = await loadAlertChannels();
  expect(v.provider).toEqual(EVOLUTION);
  expect(v.linked).toBe(true);
  expect(v.canLink).toBe(true);
  expect(v.hubKey).toEqual({ state: 'off', keyId: 'k-hub' });
});

test('the switch reads on when the bundle delegates this hub key', async () => {
  core.remoteRelays.mockResolvedValue(ownerView({ hubKey: { keyId: 'k-hub', label: 'WhatsApp' } }));
  expect((await loadAlertChannels()).hubKey).toEqual({ state: 'on', keyId: 'k-hub' });
  // An older hub key (a rotated seed) is not this one: off, to switch on again.
  core.remoteRelays.mockResolvedValue(ownerView({ hubKey: { keyId: 'k-old' } }));
  expect((await loadAlertChannels()).hubKey.state).toBe('off');
});

test('no seed on the hub, no relay, or not the owner: no switch to use', async () => {
  mockHub.hubKey.mockResolvedValue({ enabled: false });
  expect((await loadAlertChannels()).hubKey.state).toBe('unavailable');

  mockHub.hubKey.mockResolvedValue(KEY);
  core.remoteRelays.mockResolvedValue(ownerView({ relays: [] }));
  expect((await loadAlertChannels()).hubKey.state).toBe('no_relay');

  mockHub.members.mockResolvedValue({ household: { id: 'T1', name: 'Foyer' }, you: you('viewer'), members: [] });
  mockHub.whatsapp.mockResolvedValue({ provider: EVOLUTION, optIns: [] });
  const v = await loadAlertChannels();
  expect(v.hubKey.state).toBe('not_owner');
  expect(v.canLink).toBe(false); // a viewer does not route alerts to their own WhatsApp
  expect(v.linked).toBe(false);
  expect(mockHub.hubKey).toHaveBeenCalledTimes(2); // not asked for a viewer: the hub would refuse
});

test('switching on signs the hub key into the bundle as hub_block', async () => {
  await whatsapp.setHubKey(true);
  expect(core.signTrustBundle).toHaveBeenCalledTimes(1);
  const [tenant, current, change] = core.signTrustBundle.mock.calls[0];
  expect(tenant).toBe('T1');
  expect(JSON.parse(current)).toEqual(BUNDLE);
  expect(JSON.parse(change)).toEqual({ hubKey: { ed25519: 'SFVC', x25519: 'WA==', label: 'WhatsApp' } });
  expect(mockHub.putTrustBundle).toHaveBeenCalledWith('tok', 'T1', NEXT);
});

test('switching off revokes the hub key by its id', async () => {
  await whatsapp.setHubKey(false);
  expect(JSON.parse(core.signTrustBundle.mock.calls[0][2])).toEqual({ revoke: 'k-hub' });
  expect(mockHub.putTrustBundle).toHaveBeenCalledWith('tok', 'T1', NEXT);
});

test('a hub without a key cannot be switched on', async () => {
  mockHub.hubKey.mockResolvedValue({ enabled: false });
  await expect(whatsapp.setHubKey(true)).rejects.toThrow();
  expect(core.signTrustBundle).not.toHaveBeenCalled();
});

test('the opt-in asks the hub for the wa.me link', async () => {
  mockHub.whatsappOptIn.mockResolvedValue({ waLink: 'https://wa.me/221700000000?text=KERGUI%20AB3DEFGH', qrPayload: 'x', number: '+221700000000', expiresAt: 'x', provider: EVOLUTION });
  expect((await whatsapp.invite()).waLink).toContain('text=KERGUI%20');
  expect(mockHub.whatsappOptIn).toHaveBeenCalledWith('tok', 'T1');
});

test('the demo links and switches in memory, with the Evolution Go notice', () => {
  expect(DEMO_PROVIDER.notice).toContain('Evolution Go');
  const s = useDemoWhatsApp.getState();
  expect(s.linked).toBe(false);
  expect(s.hubKey).toBe(false);
  s.link();
  s.setHubKey(true);
  expect(useDemoWhatsApp.getState()).toMatchObject({ linked: true, hubKey: true });
  expect(useDemoWhatsApp.getState().message).toMatch(/^KERGUI [A-Z0-9]{4,}$/);
});
