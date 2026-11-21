/* eslint-disable import/first -- the mocks must come before the modules they replace */
// A locked action shows one neutral sentence and opens nothing: the Play app
// has no sales screen to send anyone to (RFC-006, decision of 2026-11-21).
import { act, useEffect } from 'react';
import { Alert } from 'react-native';
import { createRoot, type Root } from 'test-renderer';

const mockPush = jest.fn();
jest.mock('expo-router', () => ({ router: { push: (...a: unknown[]) => mockPush(...a), replace: jest.fn() } }));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../core/client', () => ({ api: { inspect: jest.fn() }, core: {} }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKey: null } }));

import { useEntitlement } from '../licensing/entitlement';
import { useGate } from '../licensing/useGate';
import { useSession } from '../session/store';

const NEUTRAL = 'Cette fonctionnalité n’est pas disponible pour votre compte.';

let gate: ReturnType<typeof useGate>;
let root: Root;

function Probe({ onGate }: { onGate: (g: ReturnType<typeof useGate>) => void }) {
  const g = useGate();
  useEffect(() => {
    onGate(g);
  });
  return null;
}

async function mount() {
  root = createRoot();
  await act(async () => root.render(<Probe
        onGate={(g) => {
          gate = g;
        }}
      />));
}

let alert: jest.SpyInstance;
beforeEach(() => {
  mockPush.mockReset();
  alert = jest.spyOn(Alert, 'alert').mockImplementation(() => undefined);
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: 'x' } });
  useEntitlement.setState({ boxId: 'ZTEG00000001', claims: null, token: null });
});

afterEach(async () => {
  await act(async () => root.unmount());
  alert.mockRestore();
});

test('a locked action shows the neutral message and opens no screen', async () => {
  await mount();
  const run = jest.fn();
  await act(async () => void gate.guard('block', run));
  expect(run).not.toHaveBeenCalled();
  expect(alert).toHaveBeenCalledTimes(1);
  const [title, message, buttons] = alert.mock.calls[0];
  expect(`${title}\n${message}`).toContain(NEUTRAL);
  // One plain dismiss button: no "see offers", "subscribe" or similar.
  expect(buttons ?? [{}]).toHaveLength(1);
  expect(mockPush).not.toHaveBeenCalled();
});

test('a free action runs without a message', async () => {
  await mount();
  const run = jest.fn();
  await act(async () => void gate.guard('unblock', run));
  expect(run).toHaveBeenCalled();
  expect(alert).not.toHaveBeenCalled();
});

test('demo mode opens everything (store reviewers)', async () => {
  useSession.setState({ session: { mode: 'demo' } });
  await mount();
  const run = jest.fn();
  await act(async () => void gate.guard('wifi_password', run));
  expect(run).toHaveBeenCalled();
  expect(alert).not.toHaveBeenCalled();
});
