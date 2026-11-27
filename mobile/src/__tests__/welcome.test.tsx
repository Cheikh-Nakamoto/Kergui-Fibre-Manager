/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The welcome screen (#116, audit M9): two choices, « Connecter ma box » and
// « Essayer la démo ». « Ce téléphone servira de relais » and « J'ai perdu mon
// téléphone » wait under a small « Autres choix » link. « J'ai perdu mon
// téléphone » first says the ways back, asks for nothing, then leads to the
// existing « Téléphone perdu » screen.
import { act } from 'react';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn(), canGoBack: () => true },
  Stack: { Screen: () => null },
  Redirect: () => null,
}));
jest.mock('../core/client', () => ({ api: {}, core: {} }));

import { router } from 'expo-router';

import Welcome from '../app/index';
import LostPhone from '../app/lost-phone';
import { useSession } from '../session/store';

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) {
    if (typeof c === 'string') out.push(c);
    else out.push(...texts(c));
  }
  return out;
}

let root: Root | null = null;

async function settle() {
  for (let i = 0; i < 5; i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 5))));
}

const all = (id: string) => root!.container.queryAll((n) => n.props.testID === id);
const text = () => texts(root!.container).join('\n');
const press = async (id: string) => {
  const node = all(id).find((n) => typeof n.props.onClick === 'function' || typeof n.props.onPress === 'function');
  if (!node) throw new Error(`no ${id}`);
  const ev = { nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined };
  await act(async () => void (node.props.onClick ?? node.props.onPress)(ev));
  await settle();
};

async function render(screen: React.ReactElement) {
  root = createRoot();
  await act(async () => {
    root!.render(<PaperProvider settings={{ icon: () => null }}>{screen}</PaperProvider>);
  });
  await settle();
}

beforeEach(() => {
  (router.push as jest.Mock).mockClear();
  useSession.setState({ session: null, bootError: null });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
});

test('the welcome screen offers two choices, the rest under « Autres choix »', async () => {
  await render(<Welcome />);
  expect(text()).toContain('Connecter ma box');
  expect(text()).toContain('Essayer la démo');
  expect(text()).toContain('Autres choix');
  expect(all('welcome-relay')).toHaveLength(0);
  expect(all('welcome-lost')).toHaveLength(0);
  expect(text()).not.toContain('relais');
  expect(text()).not.toContain('perdu');

  await press('welcome-more');
  expect(text()).toContain('Ce téléphone servira de relais');
  expect(text()).toContain('J’ai perdu mon téléphone');
});

test('« Ce téléphone servira de relais » opens the relay phone’s flow', async () => {
  await render(<Welcome />);
  await press('welcome-more');
  await press('welcome-relay');
  expect(router.push).toHaveBeenCalledWith('/become-relay');
});

test('« J’ai perdu mon téléphone » opens the screen of the ways back', async () => {
  await render(<Welcome />);
  await press('welcome-more');
  await press('welcome-lost');
  expect(router.push).toHaveBeenCalledWith('/lost-phone');
});

test('the lost-phone screen says the ways back before asking anything, then leads on', async () => {
  await render(<LostPhone />);
  expect(all('lost-phone').length).toBeGreaterThan(0);
  // A co-owner, at home with the box password, a cable: in plain words.
  expect(text()).toContain('Un co-propriétaire');
  expect(text()).toContain('Récupérer à la maison');
  expect(text()).toContain('mot de passe de la box');
  expect(text()).toContain('câble');
  // Nothing asked yet: no field, no sign-in.
  expect(root!.container.queryAll((n) => typeof n.props.onChangeText === 'function')).toHaveLength(0);
  expect(router.push).not.toHaveBeenCalled();

  await press('lost-phone-next');
  expect(router.push).toHaveBeenCalledWith('/recovery');
});
