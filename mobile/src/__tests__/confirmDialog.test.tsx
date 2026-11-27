// The confirmation window (#119, audit H7): a Paper Dialog instead of the
// system alert, whose « destructive » style Android ignores. A title that
// names the action, one line of consequence, a red filled button with its
// icon, « Annuler » as a text button. The same `await confirm(...)` as
// before: one host in the root layout answers it.
import { act } from 'react';
import { Alert, Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

import { confirm } from '../ui/confirm';
import { ConfirmHost } from '../ui/ConfirmHost';
import { semantic, themes } from '../ui/theme';

const iconAsText = ({ name }: { name: string }) => <Text>{`icon:${name}`}</Text>;

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) {
    if (typeof c === 'string') out.push(c);
    else out.push(...texts(c));
  }
  return out;
}

/** Every style a node carries, flattened (Paper nests arrays). */
function styles(node: TestInstance): Record<string, unknown>[] {
  const flat = (s: unknown): Record<string, unknown>[] =>
    Array.isArray(s) ? s.flatMap(flat) : s && typeof s === 'object' ? [s as Record<string, unknown>] : [];
  return flat(node.props.style);
}

let root: Root | null = null;
let alert: jest.SpyInstance;

async function settle() {
  for (let i = 0; i < 5; i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 10))));
}

async function renderHost(scheme: 'light' | 'dark' = 'light') {
  root = createRoot();
  await act(async () => {
    root!.render(
      <PaperProvider theme={themes(scheme).paper} settings={{ icon: iconAsText }}>
        <ConfirmHost />
      </PaperProvider>,
    );
  });
  return root;
}

const byId = (id: string) => root!.container.queryAll((n) => n.props.testID === id)[0];
const text = () => texts(root!.container).join('\n');
const press = async (id: string) => {
  const node = byId(id);
  await act(async () => void node.props.onClick({ nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined }));
  await settle();
};

beforeEach(() => {
  alert = jest.spyOn(Alert, 'alert').mockImplementation(() => undefined);
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
  alert.mockRestore();
});

const CUT = {
  title: 'Couper Internet pour toute la maison ?',
  message: 'Les relais et les appareils protégés le gardent.',
  confirmLabel: 'Oui, couper Internet',
  destructive: true,
  icon: 'web-off',
};

test('a confirmation is a titled dialog, not the system alert', async () => {
  await renderHost();
  let answer: Promise<boolean> = Promise.resolve(true);
  await act(async () => void (answer = confirm(CUT)));
  await settle();

  expect(alert).not.toHaveBeenCalled();
  expect(byId('confirm-dialog')).toBeDefined();
  expect(texts(byId('confirm-title')).join('')).toBe(CUT.title);
  expect(texts(byId('confirm-message')).join('')).toBe(CUT.message);
  expect(texts(byId('confirm-ok')).join('')).toContain('Oui, couper Internet');
  expect(texts(byId('confirm-cancel')).join('')).toBe('Annuler');

  await press('confirm-cancel');
  await expect(answer).resolves.toBe(false);
  expect(text()).not.toContain(CUT.title);
});

test('the destructive button is red and filled (the « blocked » role), with its icon', async () => {
  for (const scheme of ['light', 'dark'] as const) {
    await renderHost(scheme);
    await act(async () => void confirm(CUT));
    await settle();
    const container = byId('confirm-ok-container');
    expect(styles(container).some((s) => s.backgroundColor === semantic[scheme].blocked.color)).toBe(true);
    expect(texts(byId('confirm-ok')).join('')).toContain('icon:web-off');
    // « Annuler » is a text button: no fill at all.
    expect(styles(byId('confirm-cancel-container')).some((s) => s.backgroundColor === semantic[scheme].blocked.color)).toBe(false);
    await press('confirm-cancel');
    await act(async () => root?.unmount());
  }
});

test('the action button answers yes', async () => {
  await renderHost();
  let answer: Promise<boolean> = Promise.resolve(false);
  await act(async () => void (answer = confirm(CUT)));
  await settle();
  await press('confirm-ok');
  await expect(answer).resolves.toBe(true);
});

test('a reversible action is not red', async () => {
  await renderHost();
  await act(async () => void confirm({ title: 'Renommer « A » ?', message: 'Le nom change partout.', confirmLabel: 'Oui, renommer' }));
  await settle();
  expect(styles(byId('confirm-ok-container')).some((s) => s.backgroundColor === semantic.light.blocked.color)).toBe(false);
  await press('confirm-cancel');
});

test('the former call (message, label, destructive) opens the same dialog', async () => {
  await renderHost();
  let answer: Promise<boolean> = Promise.resolve(false);
  await act(async () => void (answer = confirm('Bloquer Télé ?', 'Oui, bloquer', true)));
  await settle();
  expect(texts(byId('confirm-message')).join('')).toBe('Bloquer Télé ?');
  expect(texts(byId('confirm-ok')).join('')).toContain('Oui, bloquer');
  expect(styles(byId('confirm-ok-container')).some((s) => s.backgroundColor === semantic.light.blocked.color)).toBe(true);
  await press('confirm-ok');
  await expect(answer).resolves.toBe(true);
});

test('a second confirmation answers the first one « no »', async () => {
  await renderHost();
  let first: Promise<boolean> = Promise.resolve(true);
  await act(async () => void (first = confirm('Premier ?')));
  await act(async () => void confirm('Second ?'));
  await settle();
  await expect(first).resolves.toBe(false);
  expect(texts(byId('confirm-message')).join('')).toBe('Second ?');
  await press('confirm-cancel');
});

test('with no host on screen (a background task), it falls back to the system alert', async () => {
  alert.mockImplementation((_t, _m, buttons?: { onPress?: () => void }[]) => buttons?.[1]?.onPress?.());
  await expect(confirm(CUT)).resolves.toBe(true);
  const [title, message, buttons] = alert.mock.calls[0];
  expect(title).toBe(CUT.title);
  expect(message).toBe(CUT.message);
  expect(buttons[1]).toMatchObject({ text: 'Oui, couper Internet', style: 'destructive' });
});
