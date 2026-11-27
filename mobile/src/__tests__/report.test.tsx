/* eslint-disable import/first -- the mocks must come before the modules they replace */
// « Signaler un problème » (issue #84, parent #68): Réglages opens a form —
// a category (required), a message, the app's journal only when the box is
// ticked — sent to the hub's POST /licensing/v1/complaints with the kgs_
// session; the acknowledgement shows the complaint number. The demo shows
// the form but sends nothing. The team's answer arrives as a push
// {kind: "reply", number, title, body, …}, lands in the notifications, and
// the customer answers on that complaint's thread.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

const mockReceived: ((n: unknown) => void)[] = [];
jest.mock('expo-notifications', () => ({
  addNotificationReceivedListener: (fn: (n: unknown) => void) => {
    mockReceived.push(fn);
    return { remove: () => mockReceived.splice(mockReceived.indexOf(fn), 1) };
  },
  getPresentedNotificationsAsync: async () => [],
}));
const mockPush = jest.fn();
jest.mock('expo-router', () => ({
  router: { push: (...a: unknown[]) => mockPush(...a), replace: jest.fn(), back: jest.fn(), canGoBack: () => false },
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
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));

import KerguiCore from '../../modules/kergui-core';
import { useInbox } from '../alerts/inbox';
import { useInboxFeed } from '../alerts/inboxFeed';
import Notifications from '../app/notifications';
import Report from '../app/report';
import { useAccount } from '../licensing/account';
import { config as mockConfig } from '../licensing/config';
import { setHubSession } from '../licensing/hub';
import { useSession } from '../session/store';

// --- the hub, mocked at fetch --------------------------------------------------------

type Call = { url: string; method: string; body: Record<string, unknown> | null; auth: string };
const calls: Call[] = [];
let answer: (c: Call) => { status: number; body: unknown } = () => ({ status: 500, body: {} });

const fetchMock = jest.fn(async (url: string, init: RequestInit) => {
  const c: Call = {
    url,
    method: String(init.method),
    body: init.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : null,
    auth: (init.headers as Record<string, string>).Authorization ?? '',
  };
  calls.push(c);
  const a = answer(c);
  return {
    status: a.status,
    text: async () => JSON.stringify(a.body),
    headers: { get: () => null },
  } as unknown as Response;
});

const complaintCalls = () => calls.filter((c) => c.url.includes('/complaints'));

const RECEIPT = { id: 'c-1', number: 12, status: 'open', category: 'box', source: 'app', createdAt: '2026-11-24T10:00:00Z', hasJournal: false };

// --- helpers --------------------------------------------------------------------------

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) out.push(...(typeof c === 'string' ? [c] : texts(c)));
  return out;
}
let root: Root | null = null;
const text = () => texts(root!.container).join('\n');
const all = (id: string) => root!.container.queryAll((n) => n.props.testID === id);
const settle = async (until: () => boolean = () => false) => {
  for (let i = 0; i < 30 && !until(); i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 5))));
};
const press = async (id: string) => {
  const node = all(id).find((n) => typeof n.props.onClick === 'function' || typeof n.props.onPress === 'function');
  if (!node) throw new Error(`no ${id}`);
  const ev = { nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined, preventDefault: () => undefined };
  await act(async () => void (node.props.onClick ?? node.props.onPress)(ev));
  await settle();
};
const type = async (id: string, value: string) => {
  const node = all(id).find((n) => typeof n.props.onChangeText === 'function');
  if (!node) throw new Error(`no input ${id}`);
  await act(async () => void node.props.onChangeText(value));
};
const iconAsText = ({ name }: { name: string }) => <Text>{`icon:${name}`}</Text>;

async function render(screen: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <PaperProvider settings={{ icon: iconAsText }}>{screen}</PaperProvider>
      </QueryClientProvider>,
    );
  });
  await settle();
}

const signedIn = () => {
  useAccount.setState({ ready: true, account: { accountId: 'acc_1', phone: '+221771234567', sessionToken: 'kgs_1', expiresAt: 'x' } });
  setHubSession('kgs_1');
};

beforeEach(() => {
  calls.length = 0;
  mockReceived.length = 0;
  mockPush.mockReset();
  fetchMock.mockClear();
  global.fetch = fetchMock as unknown as typeof fetch;
  mockConfig.hubUrl = 'https://hub.test';
  answer = (c) => (c.method === 'POST' && c.url.endsWith('/licensing/v1/complaints') ? { status: 201, body: RECEIPT } : { status: 404, body: {} });
  (KerguiCore.journal as jest.Mock).mockReset().mockResolvedValue(
    JSON.stringify([
      { seq: 1, time: '2026-11-24T09:59:00Z', level: 'info', msg: 'GET /devices 200' },
      { seq: 2, time: '2026-11-24T10:00:00Z', level: 'error', msg: 'box unreachable' },
    ]),
  );
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  useInbox.setState({ items: [], loaded: false });
  signedIn();
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
  setHubSession(null);
});

// --- the form -------------------------------------------------------------------------

test('the four categories are offered and one is required', async () => {
  await render(<Report />);
  const shown = text();
  for (const label of ['Mon compte', 'Box / connexion', 'Application', 'Autre']) expect(shown).toContain(label);

  await type('report-message', 'La box ne répond plus depuis ce matin.');
  await press('report-send');

  expect(fetchMock).not.toHaveBeenCalled();
  expect(text()).toContain('Choisissez une catégorie');
});

test('a message is required too', async () => {
  await render(<Report />);
  await press('report-category-box');
  await press('report-send');
  expect(fetchMock).not.toHaveBeenCalled();
  expect(text()).toContain('Décrivez le problème');
});

test('without the box ticked, no journal is sent; the number is acknowledged', async () => {
  await render(<Report />);
  await press('report-category-box');
  await type('report-message', 'La box ne répond plus depuis ce matin.');
  await press('report-send');
  await settle(() => text().includes('n° 12'));

  expect(calls).toHaveLength(1);
  const [c] = calls;
  expect(c.url).toBe('https://hub.test/licensing/v1/complaints');
  expect(c.method).toBe('POST');
  expect(c.auth).toBe('Bearer kgs_1');
  expect(c.body).toEqual({ category: 'box', message: 'La box ne répond plus depuis ce matin.', source: 'app' });
  expect(c.body).not.toHaveProperty('journal');
  expect(KerguiCore.journal).not.toHaveBeenCalled();
  expect(text()).toContain('n° 12');
});

test('with the box ticked, the journal goes with the complaint', async () => {
  answer = () => ({ status: 201, body: { ...RECEIPT, category: 'application', hasJournal: true } });
  await render(<Report />);
  await press('report-category-application');
  await type('report-message', 'L’app se ferme toute seule.');
  await press('report-journal');
  await press('report-send');
  await settle(() => calls.length > 0);

  expect(calls).toHaveLength(1);
  const body = calls[0].body!;
  expect(body.category).toBe('application');
  expect(typeof body.journal).toBe('string');
  expect(body.journal).toContain('box unreachable');
  expect(body.journal).toContain('GET /devices 200');
});

test('the journal box can be unticked again', async () => {
  await render(<Report />);
  await press('report-category-compte');
  await type('report-message', 'Mon pass n’apparaît pas.');
  await press('report-journal');
  await press('report-journal');
  await press('report-send');
  await settle(() => calls.length > 0);
  expect(calls[0].body).toEqual({ category: 'compte', message: 'Mon pass n’apparaît pas.', source: 'app' });
});

test('the hub’s refusal is worded', async () => {
  answer = () => ({ status: 429, body: { error: { code: 'COMPLAINT_RATE_LIMITED', message: 'too many' } } });
  await render(<Report />);
  await press('report-category-autre');
  await type('report-message', 'Une question.');
  await press('report-send');
  await settle(() => calls.length > 0);
  expect(text()).toContain('Trop de messages');
});

test('the demo shows the form but sends nothing', async () => {
  useSession.setState({ session: { mode: 'demo' } });
  await render(<Report />);
  expect(text()).toContain('De quoi s’agit-il ?');
  await press('report-category-box');
  await type('report-message', 'Test.');
  await press('report-send');
  expect(fetchMock).not.toHaveBeenCalled();
  expect(text()).toContain('mode démo');
});

test('the demo says so even in a build without a hub (the CI build)', async () => {
  mockConfig.hubUrl = '';
  useSession.setState({ session: { mode: 'demo' } });
  await render(<Report />);
  await press('report-category-box');
  await type('report-message', 'Test.');
  await press('report-send');
  expect(fetchMock).not.toHaveBeenCalled();
  expect(text()).toContain('mode démo');
});

test('a build without a hub refuses to send', async () => {
  mockConfig.hubUrl = '';
  await render(<Report />);
  await press('report-category-box');
  await type('report-message', 'Test.');
  await press('report-send');
  expect(fetchMock).not.toHaveBeenCalled();
  expect(all('report-blocked').length).toBeGreaterThan(0);
});

test('signed out, the screen asks to sign in and sends nothing', async () => {
  useAccount.setState({ ready: true, account: null });
  setHubSession(null);
  await render(<Report />);
  await press('report-category-box');
  await type('report-message', 'Test.');
  await press('report-send');
  expect(fetchMock).not.toHaveBeenCalled();
  expect(text()).toContain('Connectez-vous');
  // A button where « Réglages > Compte » was written (#124, audit M3).
  expect(texts(all('report-blocked')[0]).join('')).not.toMatch(/Réglages|>/);
  mockPush.mockClear();
  await press('report-sign-in');
  expect(mockPush).toHaveBeenCalledWith('/sign-in');
});

// --- the answer, in the notifications ----------------------------------------------

// What the hub pushes (internal/hub/complaints, deliver).
const reply = (date: number, identifier = 'expo-r-1') => ({
  date,
  request: {
    identifier,
    content: {
      title: 'Réponse à votre demande n° 12',
      body: 'Nous avons redémarré votre box.',
      data: {
        kind: 'reply',
        complaintId: 'c-1',
        number: '12',
        category: 'box',
        status: 'answered',
        title: 'Réponse à votre demande n° 12',
        body: 'Nous avons redémarré votre box. Dites-nous si cela va mieux.',
        messageId: '77',
        tenantId: 'T1',
      },
    },
  },
});

function App() {
  useInboxFeed();
  return <Notifications />;
}

test('an answer lands in the notifications and can be answered on its complaint', async () => {
  answer = (c) =>
    c.method === 'POST' && c.url.endsWith('/licensing/v1/complaints/12/messages')
      ? { status: 201, body: { ...RECEIPT, status: 'open', messages: [] } }
      : { status: 404, body: {} };
  await render(<App />);
  await settle(() => useInbox.getState().loaded);

  await act(async () => mockReceived.forEach((fn) => fn(reply(Date.now()))));
  await settle(() => all('inbox-reply:77').length > 0);

  const shown = text();
  expect(shown).toContain('Réponse à votre demande n° 12');
  expect(shown).toContain('Nous avons redémarré votre box. Dites-nous si cela va mieux.');
  expect(shown).toContain('Réponse de Kergui');
  expect(shown).not.toContain('Appareil');
  expect(useInbox.getState().items[0]).toMatchObject({ kind: 'reply', mac: '', complaint: 12 });

  // Received again (read back from the shade): still one entry.
  await act(async () => mockReceived.forEach((fn) => fn(reply(Date.now(), 'other-id'))));
  await settle();
  expect(useInbox.getState().items.filter((e) => e.kind === 'reply')).toHaveLength(1);

  // The customer answers on the thread.
  await press('inbox-answer-reply:77');
  await type('inbox-answer-input-reply:77', 'Oui, ça marche maintenant, merci.');
  await press('inbox-answer-send-reply:77');
  await settle(() => complaintCalls().length > 0);

  // (The screen's device list may ask the hub for its own session: not ours.)
  const sent = complaintCalls();
  expect(sent).toHaveLength(1);
  expect(sent[0].url).toBe('https://hub.test/licensing/v1/complaints/12/messages');
  expect(sent[0].method).toBe('POST');
  expect(sent[0].auth).toBe('Bearer kgs_1');
  expect(sent[0].body).toEqual({ message: 'Oui, ça marche maintenant, merci.', source: 'app' });
  await settle(() => text().includes('Message envoyé'));
  expect(text()).toContain('Message envoyé sur la demande n° 12.');
  expect(mockPush).not.toHaveBeenCalled();
});

test('an empty answer is not sent', async () => {
  await render(<App />);
  await settle(() => useInbox.getState().loaded);
  await act(async () => mockReceived.forEach((fn) => fn(reply(Date.now()))));
  await settle(() => all('inbox-reply:77').length > 0);
  await press('inbox-answer-reply:77');
  await type('inbox-answer-input-reply:77', '   ');
  await press('inbox-answer-send-reply:77');
  expect(complaintCalls()).toHaveLength(0);
});

test('an answer on a closed complaint is worded', async () => {
  answer = (c) =>
    c.url.includes('/complaints/') ? { status: 409, body: { error: { code: 'COMPLAINT_CLOSED', message: 'closed' } } } : { status: 404, body: {} };
  await render(<App />);
  await settle(() => useInbox.getState().loaded);
  await act(async () => mockReceived.forEach((fn) => fn(reply(Date.now()))));
  await settle(() => all('inbox-reply:77').length > 0);
  await press('inbox-answer-reply:77');
  await type('inbox-answer-input-reply:77', 'Encore une question.');
  await press('inbox-answer-send-reply:77');
  await settle(() => text().includes('close'));
  expect(text()).toContain('Cette demande est close');
});

test('a long journal is cut to the hub’s 64 KiB, the recent end kept', async () => {
  const { journalText } = jest.requireActual<typeof import('../features/complaints')>('../features/complaints');
  const lines = Array.from({ length: 2000 }, (_, i) => ({ seq: i, time: '2026-11-24T10:00:00Z', level: 'info', msg: `ligne ${i} é ${'x'.repeat(40)}` }));
  (KerguiCore.journal as jest.Mock).mockResolvedValue(JSON.stringify(lines));
  const text = await journalText();
  expect(Buffer.byteLength(text, 'utf8')).toBeLessThanOrEqual(64 * 1024);
  expect(text).toContain('ligne 1999 ');
  expect(text).not.toContain('ligne 0 ');
});
