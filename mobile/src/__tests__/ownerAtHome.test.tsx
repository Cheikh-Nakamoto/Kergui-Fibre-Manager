/* eslint-disable import/first -- the mocks must come before the modules they replace */
// « Proprio prioritaire » (#94): the owner's phone at home, on the box's own
// network, talks to the box directly and its session wins; away, it goes
// through the relay. Walking out of the Wi-Fi or back in switches without an
// error on screen and without losing the list. A member's phone always goes
// through the relay, and is told when the relay waits for the owner to leave.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
  Stack: { Screen: () => null },
  useLocalSearchParams: () => ({}),
}));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../alerts/alerts', () => ({ checkNewDevices: async () => 0, enforceAutoReject: async () => 0 }));
jest.mock('../features/relayPolicy', () => ({ syncRelayPolicy: async () => undefined }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));
jest.mock('../licensing/household', () => ({
  withHousehold: (run: (tok: string, id: string) => Promise<unknown>) => run('tok', 'T1'),
}));
const mockHub = {
  trustBundle: jest.fn(),
  routerDevices: jest.fn(),
  relayStatus: jest.fn(),
  deviceCommand: jest.fn(),
  routerCommand: jest.fn(),
  command: jest.fn(),
};
jest.mock('../licensing/hub', () => ({
  get hub() {
    return mockHub;
  },
}));

import KerguiCore from '../../modules/kergui-core';
import Devices from '../app/(tabs)/devices';
import { RemoteCommandError } from '../core/remote';
import { AT_HOME_TIMEOUT_MS, probeAtHome } from '../features/atHome';
import { remoteRequest, type RemoteHousehold } from '../features/remote';
import { errorMessage, t } from '../i18n';
import { useAccount } from '../licensing/account';
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';

const core = KerguiCore as unknown as Record<string, jest.Mock>;
const PIN = 'ab:cd:ef:01';
const BOX = 'https://192.168.1.1';
const BUNDLE = { payload: 'UA==', sig: 'Uw==', version: 3 };
const LOCAL_PC = 'Laptop-Local';
const RELAY_PC = 'Laptop-Relais';

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) {
    if (typeof c === 'string') out.push(c);
    else out.push(...texts(c));
  }
  return out;
}

const iconAsText = ({ name }: { name: string }) => <Text>{`icon:${name}`}</Text>;
let root: Root | null = null;
let qc: QueryClient | null = null;

async function settle(until: () => boolean = () => false) {
  for (let i = 0; i < 100 && !until(); i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 10))));
}

async function render(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false, gcTime: 0 } } });
  qc = client;
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={client}>
        <PaperProvider settings={{ icon: iconAsText }}>{ui}</PaperProvider>
      </QueryClientProvider>,
    );
  });
  return root;
}

const byId = (r: Root, id: string) => r.container.queryAll((n) => n.props.testID === id)[0];
const text = (r: Root) => texts(r.container).join('\n');

// Where the phone is: at home the box answers with the pinned certificate;
// away the address does not answer.
let home = true;
// What reached the box from this phone.
let boxReads = 0;

/** What the core answers when it signs a request for the relay. */
const signed = (cmdId: string, method: string, path: string) =>
  JSON.stringify({ payload: 'Q01E', sig: 'U0lH', cmdId, method, path, expiresAt: new Date(Date.now() + 60_000).toISOString() });

function device(name: string) {
  return { MAC: 'ac:bb:cc:00:00:22', IP: '192.168.1.10', Hostname: name, CustomName: '', Vendor: '', SSID: 'MonWiFi-2G', Connected: true, Blocked: false, NewlySeen: false };
}

beforeEach(() => {
  home = true;
  boxReads = 0;
  for (const m of Object.values(mockHub)) m.mockReset();
  for (const k of ['request', 'certificateFingerprint', 'remoteRelays', 'openDeviceSnapshot', 'signRemoteRequest', 'verifyCommandResult']) core[k].mockReset();
  core.certificateFingerprint.mockImplementation(async (url: string) => {
    if (!home || url !== BOX) throw new Error('dial tcp 192.168.1.1:443: i/o timeout');
    return PIN.toUpperCase();
  });
  core.request.mockImplementation(async (method: string, path: string) => {
    if (method === 'GET' && path === '/api/devices') {
      if (!home) return { status: 502, body: '{"error":"router unreachable"}' };
      boxReads++;
      return { status: 200, body: JSON.stringify([device(LOCAL_PC)]) };
    }
    return { status: 501, body: '{"error":"not implemented"}' };
  });
  useSession.setState({ session: { mode: 'router', routerUrl: BOX, pin: PIN } });
  useAccount.setState({ account: { accountId: 'acc', phone: '+221770000001', sessionToken: 'kgs_x', expiresAt: '' } });
  useEntitlement.setState({ boxId: 'ZTEG00000001' });

  mockHub.trustBundle.mockResolvedValue(BUNDLE);
  const asOf = new Date().toISOString();
  mockHub.routerDevices.mockResolvedValue({ snapshot: { payload: 'cA==', sig: 'cw==' }, agentId: 'A1', asOf });
  mockHub.relayStatus.mockResolvedValue({ state: 'active', agentId: 'A1', lastSeenAt: asOf, ageSeconds: 5 });
  core.remoteRelays.mockResolvedValue(JSON.stringify({ owner: true, relays: [{ agentId: 'A1', kind: 'phone' }] }));
  core.openDeviceSnapshot.mockResolvedValue(JSON.stringify({ agentId: 'A1', asOf, devices: [device(RELAY_PC)] }));
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
});

describe('where the phone is', () => {
  test('at home: the box answers with the pinned certificate', async () => {
    await expect(probeAtHome()).resolves.toBe(true);
  });

  test('away: the box does not answer, or another one does', async () => {
    home = false;
    await expect(probeAtHome()).resolves.toBe(false);
    home = true;
    core.certificateFingerprint.mockResolvedValueOnce('00:11:22:33');
    await expect(probeAtHome()).resolves.toBe(false);
  });

  test('a box that hangs counts as away once the wait is over', async () => {
    jest.useFakeTimers();
    try {
      core.certificateFingerprint.mockImplementationOnce(() => new Promise(() => undefined));
      const p = probeAtHome();
      jest.advanceTimersByTime(AT_HOME_TIMEOUT_MS + 1);
      await expect(p).resolves.toBe(false);
    } finally {
      jest.useRealTimers();
    }
  });

  test('nothing to recognise the box by: never at home', async () => {
    useSession.setState({ session: { mode: 'router', routerUrl: BOX, pin: '' } });
    await expect(probeAtHome()).resolves.toBe(false);
    useSession.setState({ session: { mode: 'demo' } });
    await expect(probeAtHome()).resolves.toBe(false);
    expect(core.certificateFingerprint).not.toHaveBeenCalled();
  });
});

describe('the owner chooses the box at home and the relay away', () => {
  test('at home the list comes from the box itself, not through the relay', async () => {
    const r = await render(<Devices />);
    await settle(() => text(r).includes(LOCAL_PC));
    expect(text(r)).toContain(LOCAL_PC);
    expect(byId(r, 'remote-banner')).toBeUndefined();
    // The relay's list may be read, for the names given through it (#95),
    // but what is shown is the box's.
    expect(text(r)).not.toContain(RELAY_PC);
    expect(boxReads).toBeGreaterThan(0);
  });

  test('away the list comes through the relay, and the box is never asked', async () => {
    home = false;
    const r = await render(<Devices />);
    await settle(() => text(r).includes(RELAY_PC));
    expect(text(r)).toContain(RELAY_PC);
    expect(byId(r, 'remote-banner')).toBeDefined();
    expect(boxReads).toBe(0);
  });

  test('leaving home switches to the relay with no error left on screen', async () => {
    const r = await render(<Devices />);
    await settle(() => text(r).includes(LOCAL_PC));

    // Out of the Wi-Fi: the next refresh finds the box gone, asks again where
    // the phone is, and goes through the relay.
    home = false;
    await act(async () => void qc!.invalidateQueries({ queryKey: ['devices'] }));
    await settle(() => text(r).includes(RELAY_PC));
    expect(text(r)).toContain(RELAY_PC);
    expect(byId(r, 'remote-banner')).toBeDefined();
    expect(text(r)).not.toContain(t('error.unreachable'));
  });

  test('coming home switches back to the box', async () => {
    home = false;
    const r = await render(<Devices />);
    await settle(() => text(r).includes(RELAY_PC));

    home = true;
    await act(async () => void qc!.invalidateQueries({ queryKey: ['remote', 'atHome'] }));
    await settle(() => text(r).includes(LOCAL_PC));
    expect(text(r)).toContain(LOCAL_PC);
    expect(byId(r, 'remote-banner')).toBeUndefined();
    expect(boxReads).toBeGreaterThan(0);
  });

  test('away while the relay is still paused for the owner: a read signed by the owner takes the box back', async () => {
    home = false;
    mockHub.relayStatus.mockResolvedValue({ state: 'paused', agentId: 'A1', lastSeenAt: new Date().toISOString(), ageSeconds: 5, reason: 'the owner is using the box at home' });
    core.signRemoteRequest.mockResolvedValue(signed('c9', 'GET', '/api/devices'));
    mockHub.routerCommand.mockResolvedValue({ commandId: 'c9', status: 'queued' });
    mockHub.command.mockResolvedValue({ commandId: 'c9', status: 'expired' });
    const r = await render(<Devices />);
    await settle(() => mockHub.routerCommand.mock.calls.length > 0);
    expect(mockHub.routerCommand).toHaveBeenCalled();
    expect(text(r)).toContain(t('remote.paused'));
    expect(boxReads).toBe(0);
  });

  test('a member stays on the relay even at home', async () => {
    core.remoteRelays.mockResolvedValue(
      JSON.stringify({ owner: false, relays: [{ agentId: 'A1', kind: 'phone' }], member: { state: 'active', control: true }, ownerOnly: false }),
    );
    const r = await render(<Devices />);
    await settle(() => text(r).includes(RELAY_PC));
    expect(text(r)).toContain(RELAY_PC);
    expect(boxReads).toBe(0);
  });
});

describe('a paused relay', () => {
  const OWNER: RemoteHousehold = { tenantId: 'T1', bundle: BUNDLE, relays: [{ agentId: 'A1', kind: 'phone' }] };
  const MEMBER: RemoteHousehold = { ...OWNER, member: { control: true, ownerOnly: false } };
  const opts = { boxId: 'ZTEG00000001', sleep: async () => undefined };

  beforeEach(() => {
    mockHub.relayStatus.mockResolvedValue({ state: 'paused', agentId: 'A1', lastSeenAt: new Date().toISOString(), ageSeconds: 5 });
  });

  test('a member is told the relay waits for the owner, and nothing is sent', async () => {
    const err = await remoteRequest(MEMBER, 'GET', '/api/devices', undefined, opts).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(RemoteCommandError);
    expect((err as RemoteCommandError).reason).toBe('relay_paused');
    expect(errorMessage(err)).toBe(t('remote.error.relay_paused'));
    expect(mockHub.routerCommand).not.toHaveBeenCalled();
  });

  test("the owner's command goes: it takes the box back", async () => {
    core.signRemoteRequest.mockResolvedValue(signed('c1', 'GET', '/api/devices'));
    mockHub.routerCommand.mockResolvedValue({ commandId: 'c1', status: 'done', result: { payload: 'cg==', sig: 'cw==' } });
    core.verifyCommandResult.mockResolvedValue(JSON.stringify({ agentId: 'A1', status: 200, body: [], executedAt: new Date().toISOString() }));
    await expect(remoteRequest(OWNER, 'GET', '/api/devices', undefined, opts)).resolves.toMatchObject({ status: 200 });
  });

  test('the relay answering that the owner is home is said as such, not as a box error', async () => {
    core.signRemoteRequest.mockResolvedValue(signed('c2', 'POST', '/api/devices/ac:bb:cc:00:00:22/block'));
    mockHub.relayStatus.mockResolvedValue({ state: 'active', agentId: 'A1' });
    mockHub.routerCommand.mockResolvedValue({ commandId: 'c2', status: 'done', result: { payload: 'cg==', sig: 'cw==' } });
    core.verifyCommandResult.mockResolvedValue(
      JSON.stringify({ agentId: 'A1', status: 503, body: { code: 'owner_at_home', error: 'paused' }, executedAt: new Date().toISOString() }),
    );
    const err = await remoteRequest(MEMBER, 'POST', '/api/devices/ac:bb:cc:00:00:22/block', undefined, opts).catch((e: unknown) => e);
    expect((err as RemoteCommandError).reason).toBe('relay_paused');
  });
});
