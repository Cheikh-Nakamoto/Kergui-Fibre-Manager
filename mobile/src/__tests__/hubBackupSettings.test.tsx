/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The household's backup on the hub (issue #89), as the app wires it: a sign-in
// on an empty phone fetches the box's backup and hands it to the core's
// import; Réglages' « Sauvegarder maintenant » sends the core's export, and
// refuses in the demo.
import { act } from 'react';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type TestInstance } from 'test-renderer';

const mockStore: Record<string, string> = {};
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async (k: string) => mockStore[k] ?? null,
  setItemAsync: async (k: string, v: string) => void (mockStore[k] = v),
  deleteItemAsync: async (k: string) => void delete mockStore[k],
}));
jest.mock('expo-file-system', () => ({ Paths: { cache: 'cache' }, File: class {} }));
jest.mock('expo-sharing', () => ({ shareAsync: jest.fn() }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));
jest.mock('../licensing/hub', () => ({
  hub: { backup: jest.fn(), putBackup: jest.fn() },
  hubTokens: {},
  setHubSession: () => undefined,
}));
jest.mock('../licensing/household', () => ({
  withHousehold: (run: (tok: string, id: string) => Promise<unknown>) => run('jwt', 'T1'),
}));

import KerguiCore from '../../modules/kergui-core';
import { hubBackup } from '../features/hubBackup';
import { useAccount } from '../licensing/account';
import { hub } from '../licensing/hub';
import { useSession } from '../session/store';
import { HubBackup } from '../ui/HubBackup';

const request = KerguiCore.request as jest.Mock;
const mockHub = hub as unknown as { backup: jest.Mock; putBackup: jest.Mock };
const ACCOUNT = { accountId: 'acc_1', phone: '+221771234567', sessionToken: 'kgs_1', expiresAt: 'x' };

function coreAnswers() {
  request.mockImplementation(async (method: string, path: string) => {
    if (method === 'GET' && path === '/api/export')
      return { status: 200, body: JSON.stringify({ archive: 'UEsDBA-phone', name: 'k.zip', boxId: '192-168-1-1' }) };
    if (method === 'POST' && path === '/api/import') return { status: 200, body: JSON.stringify({ devices: 3, named: 2, usage: 1 }) };
    return { status: 404, body: '{}' };
  });
}

async function render() {
  const r = createRoot();
  await act(async () =>
    r.render(
      <PaperProvider>
        <HubBackup />
      </PaperProvider>,
    ),
  );
  return r;
}

const byId = (r: ReturnType<typeof createRoot>, id: string) =>
  r.container.queryAll((n: TestInstance) => n.props.testID === id && typeof (n.props.onPress ?? n.props.onClick) === 'function')[0];
const press = async (r: ReturnType<typeof createRoot>, id: string) => {
  const n = byId(r, id);
  await act(async () => (n.props.onPress ?? n.props.onClick)());
};
const textOf = (r: ReturnType<typeof createRoot>) =>
  r.container
    .queryAll(() => true)
    .flatMap((n) => [n.props.children].flat())
    .filter((c): c is string => typeof c === 'string')
    .join('\n');

beforeEach(() => {
  request.mockReset();
  mockHub.backup.mockReset();
  mockHub.putBackup.mockReset();
  for (const k of Object.keys(mockStore)) delete mockStore[k];
  coreAnswers();
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
});

test('sign-in on an empty phone fetches the box backup and imports it', async () => {
  useAccount.setState({ ready: true, account: null });
  mockHub.backup.mockResolvedValue({ boxId: '192-168-1-1', updatedAt: '2026-11-20T08:00:00Z', archive: 'UEsDBA-hub', name: 'k.zip' });
  await act(async () => useAccount.setState({ account: ACCOUNT }));
  await hubBackup.tick();
  expect(mockHub.backup).toHaveBeenCalledWith('jwt', 'T1', '192-168-1-1');
  expect(request).toHaveBeenCalledWith('POST', '/api/import', JSON.stringify({ archive: 'UEsDBA-hub' }));
});

test('« Sauvegarder maintenant » sends the core export and says when', async () => {
  useAccount.setState({ ready: true, account: ACCOUNT });
  mockHub.putBackup.mockResolvedValue({ boxId: '192-168-1-1', updatedAt: '2026-11-24T10:00:00Z', merged: false });
  const r = await render();
  await press(r, 'settings-hub-backup');
  expect(mockHub.putBackup).toHaveBeenCalledWith('jwt', 'T1', '192-168-1-1', 'UEsDBA-phone');
  expect(textOf(r)).toContain('Sauvegardé sur Kergui');
  expect(textOf(r)).toContain('Dernière sauvegarde : 24/11/2026');
});

test('in the demo « Sauvegarder maintenant » refuses and sends nothing', async () => {
  useAccount.setState({ ready: true, account: null });
  useSession.setState({ session: { mode: 'demo' } as never });
  const r = await render();
  expect(byId(r, 'settings-hub-backup-download')).toBeUndefined();
  await press(r, 'settings-hub-backup');
  expect(mockHub.putBackup).not.toHaveBeenCalled();
  expect(textOf(r)).toContain('En mode démo, rien n’est envoyé à Kergui');
});
