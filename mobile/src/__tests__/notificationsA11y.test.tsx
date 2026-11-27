/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The notifications screen within reach (#125, audit M7, L4): the « × » of
// every row and the inbox's buttons are 48 × 48 dp targets (Android's
// accessibility help; WCAG 2.2 SC 2.5.8 at the very least), « Supprimer cette
// notification » and « Tout effacer » are undone with « Annuler » rather
// than confirmed, and Kergui's own news (an update, a message, an answer)
// wears a neutral badge, not the green of « connecté », with its kind read
// out.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, type ReactNode } from 'react';
import { Alert, StyleSheet, Text, type ViewStyle } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
  // The header's « Tout effacer » is drawn where the test can reach it.
  Stack: { Screen: ({ options }: { options?: { headerRight?: () => ReactNode } }) => options?.headerRight?.() ?? null },
  useLocalSearchParams: () => ({}),
}));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../alerts/alerts', () => ({ checkNewDevices: async () => 0, enforceAutoReject: async () => 0 }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: '', publicKeys: [] } }));

import KerguiCore from '../../modules/kergui-core';
import { useInbox, type InboxEntry } from '../alerts/inbox';
import Journal from '../app/journal';
import Notifications from '../app/notifications';
import { useSession } from '../session/store';
import { useConfirm } from '../ui/confirm';
import { semantic } from '../ui/theme';
import { slopFor, TARGET } from '../ui/targets';

const request = KerguiCore.request as jest.Mock;
const VISITOR = '02:4b:47:00:00:58';
const NOW = Date.now();

const ENTRIES: Omit<InboxEntry, 'read'>[] = [
  { id: `arrival:${VISITOR}:1`, kind: 'arrival', mac: VISITOR, name: 'Tablette-Visiteur', at: NOW - 1000 },
  { id: 'refused:02:4b:47:00:00:59:1', kind: 'refused', mac: '02:4b:47:00:00:59', name: 'Inconnu', at: NOW - 2000 },
  { id: 'update:42', kind: 'update', mac: '', name: '1.4.0', at: NOW - 3000 },
  { id: 'message:7', kind: 'message', mac: '', name: 'Coupure prévue', text: 'Samedi, 2 h.', at: NOW - 4000 },
  { id: 'reply:9', kind: 'reply', mac: '', name: 'Réponse', text: 'C’est réglé.', complaint: 12, at: NOW - 5000 },
];

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
  for (let i = 0; i < 50 && !until(); i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 10))));
}

async function render(screen: ReactNode = <Notifications />) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false, gcTime: 0 } } });
  qc = client;
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={client}>
        <PaperProvider settings={{ icon: iconAsText }}>{screen}</PaperProvider>
      </QueryClientProvider>,
    );
  });
  await settle();
  return root;
}

const byId = (r: Root, id: string) => r.container.queryAll((n) => n.props.testID === id)[0];
const byLabel = (r: Root, label: string) => r.container.queryAll((n) => n.props.accessibilityLabel === label)[0];
const text = (r: Root) => texts(r.container).join('\n');
const press = async (node: TestInstance) =>
  act(async () => void node.props.onClick({ nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined }));
const flat = (node: TestInstance) => (StyleSheet.flatten(node.props.style) ?? {}) as ViewStyle;
const ids = () => useInbox.getState().items.map((e) => e.id);

/** The touch area of a Paper control: its container, grown by the touchable's hitSlop. */
function area(r: Root, id: string, height?: number) {
  // Paper's Surface: the outer layer carries the size, the inner the minimum width.
  const box = { ...flat(byId(r, `${id}-container-outer-layer`)), ...flat(byId(r, `${id}-container`)) };
  const slop = byId(r, id).props.hitSlop ?? {};
  const dp = (v: unknown) => (typeof v === 'number' ? v : 0);
  const w = Math.max(dp(box.width), dp(box.minWidth));
  const h = dp(box.height) || (height ?? 0);
  return { width: w + (slop.left ?? 0) + (slop.right ?? 0), height: h + (slop.top ?? 0) + (slop.bottom ?? 0) };
}

beforeEach(() => {
  jest.useRealTimers();
  request.mockReset();
  request.mockImplementation(async (method: string, path: string) => {
    if (method === 'GET' && path === '/api/devices')
      return {
        status: 200,
        body: JSON.stringify([
          { MAC: VISITOR, IP: '192.168.1.58', Hostname: 'Tablette-Visiteur', CustomName: '', Connected: true, Blocked: false, NewlySeen: false, Approved: false },
        ]),
      };
    return { status: 404, body: '{"error":"not found"}' };
  });
  useInbox.setState({ items: [], loaded: true });
  useInbox.getState().add(ENTRIES);
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
});

test('every « × » is a 48 × 48 dp target', async () => {
  const r = await render();
  await settle(() => !!byId(r, `inbox-remove-${ENTRIES[4].id}`));
  for (const e of ENTRIES) {
    const a = area(r, `inbox-remove-${e.id}`);
    expect(a.width).toBeGreaterThanOrEqual(TARGET);
    expect(a.height).toBeGreaterThanOrEqual(TARGET);
    // The icon at Paper's default size (24), not the old 18.
    expect(flat(byId(r, `inbox-remove-${e.id}-container-outer-layer`)).width).toBeGreaterThanOrEqual(40);
  }
});

test('the inbox actions are regular buttons, 48 dp high with their margin', async () => {
  const r = await render();
  await settle(() => !!byId(r, `inbox-approve-${VISITOR}`));
  // MD3 buttons are 40 dp high: the hitSlop grows them to 48.
  for (const id of [`inbox-approve-${VISITOR}`, `inbox-answer-${ENTRIES[4].id}`, 'inbox-clear']) {
    const a = area(r, id, 40);
    expect(a.height).toBeGreaterThanOrEqual(TARGET);
    // Not compact: MD3's 64 dp minimum width.
    expect(a.width).toBeGreaterThanOrEqual(64);
  }
  await press(byId(r, `inbox-answer-${ENTRIES[4].id}`));
  await settle(() => !!byId(r, `inbox-answer-send-${ENTRIES[4].id}`));
  for (const id of [`inbox-answer-send-${ENTRIES[4].id}`, `inbox-answer-cancel-${ENTRIES[4].id}`]) {
    const a = area(r, id, 40);
    expect(a.height).toBeGreaterThanOrEqual(TARGET);
    expect(a.width).toBeGreaterThanOrEqual(64);
  }
});

test('« Supprimer cette notification » is undone with « Annuler »', async () => {
  const r = await render();
  const id = ENTRIES[2].id;
  await settle(() => !!byId(r, `inbox-remove-${id}`));
  await press(byId(r, `inbox-remove-${id}`));
  await settle(() => !!byId(r, 'inbox-undo'));
  expect(ids()).not.toContain(id);
  expect(byId(r, `inbox-${id}`)).toBeUndefined();
  expect(text(r)).toContain('Notification supprimée.');

  await press(byId(r, 'inbox-undo'));
  await settle(() => !!byId(r, `inbox-${id}`));
  expect(ids()).toEqual(ENTRIES.map((e) => e.id));
  // Back as it was: read, since the screen marked it so.
  expect(useInbox.getState().items.find((e) => e.id === id)?.read).toBe(true);
});

test('without « Annuler », the notification stays deleted', async () => {
  const r = await render();
  const id = ENTRIES[3].id;
  await settle(() => !!byId(r, `inbox-remove-${id}`));
  await press(byId(r, `inbox-remove-${id}`));
  await settle(() => !!byId(r, 'inbox-undo'));
  // Another deletion: only the last one is offered back.
  await press(byId(r, `inbox-remove-${ENTRIES[4].id}`));
  await settle();
  await press(byId(r, 'inbox-undo'));
  await settle(() => !!byId(r, `inbox-${ENTRIES[4].id}`));
  expect(ids()).not.toContain(id);
  expect(ids()).toContain(ENTRIES[4].id);
});

test('« Tout effacer » asks nothing and is undone with « Annuler »', async () => {
  const alert = jest.spyOn(Alert, 'alert').mockImplementation(() => undefined);
  const r = await render();
  await settle(() => !!byId(r, 'inbox-clear'));
  await press(byId(r, 'inbox-clear'));
  await settle(() => !!byId(r, 'inbox-undo'));
  // No dialog any more (#119 asked; #125 undoes instead).
  expect(useConfirm.getState().shown).toBeNull();
  expect(alert).not.toHaveBeenCalled();
  expect(ids()).toEqual([]);
  expect(text(r)).toContain('Notifications effacées.');

  await press(byId(r, 'inbox-undo'));
  await settle(() => !!byId(r, `inbox-${ENTRIES[0].id}`));
  expect(ids()).toEqual(ENTRIES.map((e) => e.id));
  alert.mockRestore();
});

test('Kergui’s own news wears a neutral badge, not the green of « connecté »', async () => {
  const r = await render();
  await settle(() => !!byId(r, `inbox-badge-${ENTRIES[4].id}`));
  const tones = semantic.light;
  for (const e of ENTRIES.slice(2)) {
    const colour = flat(byId(r, `inbox-badge-${e.id}`)).backgroundColor;
    expect(colour).toBe(tones.neutral.color);
    expect(colour).not.toBe(tones.connected.color);
  }
  expect(flat(byId(r, `inbox-badge-${ENTRIES[0].id}`)).backgroundColor).toBe(tones.attention.color);
  expect(flat(byId(r, `inbox-badge-${ENTRIES[1].id}`)).backgroundColor).toBe(tones.blocked.color);
});

test('every badge says what it marks to a screen reader', async () => {
  const r = await render();
  await settle(() => !!byId(r, `inbox-badge-${ENTRIES[4].id}`));
  const said = ENTRIES.map((e) => byId(r, `inbox-badge-${e.id}`).props.accessibilityLabel);
  expect(said).toEqual(['À vérifier', 'Refusé', 'Mise à jour', 'Message de Kergui', 'Réponse de Kergui']);
  for (const e of ENTRIES) expect(byId(r, `inbox-badge-${e.id}`).props.accessible).toBe(true);
  expect(byLabel(r, 'À vérifier')).toBeDefined();
});

test('slopFor grows a control to 48 dp, and never shrinks one', () => {
  expect(slopFor(40, 40)).toEqual({ top: 4, bottom: 4, left: 4, right: 4 });
  expect(slopFor(64, 40)).toEqual({ top: 4, bottom: 4, left: 0, right: 0 });
  expect(slopFor(56, 56)).toEqual({ top: 0, bottom: 0, left: 0, right: 0 });
});

test('the journal’s filter chips are 48 dp targets too', async () => {
  (KerguiCore.journal as jest.Mock).mockResolvedValue([]);
  const r = await render(<Journal />);
  await settle(() => !!byId(r, 'journal-problems'));
  for (const id of ['journal-all', 'journal-problems']) {
    // MD3 chips are 32 dp high.
    expect(area(r, id, 32).height).toBeGreaterThanOrEqual(TARGET);
  }
});
