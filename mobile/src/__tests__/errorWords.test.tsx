/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Errors in plain words (#119, audit H3): never a Go sentence, an HTTP code
// or the box's own text on screen. An error the app cannot name says « Ça
// n'a pas marché » with a « Signaler un problème » button; the raw text goes
// to the journal (core.note) only, where a support request can carry it.
import { act } from 'react';
import { Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

const mockPush = jest.fn();
jest.mock('expo-router', () => ({
  router: { push: (...a: unknown[]) => mockPush(...a), navigate: jest.fn(), replace: jest.fn(), back: jest.fn() },
}));

import KerguiCore from '../../modules/kergui-core';
import { ApiError } from '../core/client';
import { RemoteCommandError } from '../core/remote';
import { errorMessage, hubErrorMessage, isUnknownError } from '../i18n';
import { fr } from '../i18n/fr';
import { HubError } from '../licensing/api';
import { ActionError } from '../ui/ActionError';
import { ErrorBanner } from '../ui/ErrorBanner';

const GENERIC = 'Ça n’a pas marché. Réessayez. Si ça continue, signalez-le.';
const note = KerguiCore.note as jest.Mock;
const noted = () => note.mock.calls.map(([, line]) => String(line)).join('\n');

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) {
    if (typeof c === 'string') out.push(c);
    else out.push(...texts(c));
  }
  return out;
}

let root: Root | null = null;

async function render(ui: React.ReactElement) {
  root = createRoot();
  await act(async () => {
    root!.render(<PaperProvider settings={{ icon: ({ name }: { name: string }) => <Text>{`icon:${name}`}</Text> }}>{ui}</PaperProvider>);
  });
  return root;
}

const byId = (id: string) => root!.container.queryAll((n) => n.props.testID === id)[0];
const text = () => texts(root!.container).join('\n');

beforeEach(() => {
  note.mockReset();
  mockPush.mockReset();
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
});

test('an error the app cannot name is one plain sentence, its raw text in the journal only', () => {
  const e = new Error('dial tcp 192.168.1.1:443: i/o timeout');
  expect(errorMessage(e)).toBe(GENERIC);
  expect(isUnknownError(e)).toBe(true);
  expect(noted()).toContain('dial tcp 192.168.1.1:443: i/o timeout');
});

test('an unknown core answer shows neither its code nor its Go text', () => {
  const e = new ApiError(418, 'router: unexpected status 418 from /cgi-bin/luci');
  const said = errorMessage(e);
  expect(said).toBe(GENERIC);
  expect(said).not.toMatch(/418|router|luci/);
  expect(noted()).toContain('418');
  expect(noted()).toContain('router: unexpected status 418 from /cgi-bin/luci');
});

test('the same error shown again (a re-render) is noted once', () => {
  const e = new Error('boom');
  errorMessage(e);
  errorMessage(e);
  errorMessage(e);
  expect(note).toHaveBeenCalledTimes(1);
});

test('a known error keeps its own sentence and is no « unknown »', () => {
  const e = new ApiError(502, 'router unreachable');
  expect(errorMessage(e)).toBe(fr['error.unreachable']);
  expect(isUnknownError(e)).toBe(false);
});

test('the box refusing a remote command: no code, and when to try again', () => {
  const e = new RemoteCommandError('box', undefined, 502);
  expect(errorMessage(e)).toBe('La box n’a pas accepté. Réessayez dans une minute.');
  expect(noted()).toContain('502');
});

test('an unknown hub error is the same plain sentence', () => {
  const e = new HubError(500, 'INTERNAL' as never, 'pq: connection refused');
  expect(hubErrorMessage(e)).toBe(GENERIC);
  expect(noted()).toContain('pq: connection refused');
});

test('no French string inserts a raw error, a box reason or an HTTP status', () => {
  const raw = Object.entries(fr).filter(([, v]) => /\{(error|reason|status)\}/.test(v));
  expect(raw.map(([k]) => k)).toEqual([]);
});

test('under a failed write: the sentence and a « Signaler un problème » button to the request screen', async () => {
  await render(<ActionError error={new Error('exit status 1')} />);
  expect(text()).toContain(GENERIC);
  expect(text()).not.toContain('exit status 1');
  const button = byId('report-problem');
  expect(texts(button).join('')).toContain('Signaler un problème');
  await act(async () => void button.props.onClick({ nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined }));
  expect(mockPush).toHaveBeenCalledWith('/report');
});

test('a known error offers no report button', async () => {
  await render(<ActionError error={new ApiError(503, 'busy')} />);
  expect(text()).toContain(fr['error.busy']);
  expect(byId('report-problem')).toBeUndefined();
});

test('the banner of a failed read offers « Signaler un problème » next to « Réessayer »', async () => {
  await render(<ErrorBanner error={new Error('EOF')} onRetry={() => undefined} />);
  expect(text()).toContain(GENERIC);
  expect(text()).not.toContain('EOF');
  expect(text()).toContain('Réessayer');
  expect(text()).toContain('Signaler un problème');
});
