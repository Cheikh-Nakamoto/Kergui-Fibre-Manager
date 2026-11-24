// The privacy policy is reachable from Réglages (#37, Play's User Data
// policy): the build's hub serves it; the demo, or a build without a hub,
// opens the public hub's copy.
import { PUBLIC_HUB_URL, privacyUrl } from '../licensing/privacy';

test('the build’s hub serves the policy', () => {
  expect(privacyUrl({ hubUrl: 'https://hub.example.sn', demo: false })).toBe('https://hub.example.sn/licensing/v1/privacy');
});

test('in the demo, or without a hub, the public copy', () => {
  expect(PUBLIC_HUB_URL).toBe('https://hub.kergui.click');
  expect(privacyUrl({ hubUrl: 'https://hub.example.sn', demo: true })).toBe('https://hub.kergui.click/licensing/v1/privacy');
  expect(privacyUrl({ hubUrl: '', demo: false })).toBe('https://hub.kergui.click/licensing/v1/privacy');
});
