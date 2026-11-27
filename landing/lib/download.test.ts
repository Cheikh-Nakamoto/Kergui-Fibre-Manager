// /telecharger hands out only the APK the hub has published (a pending
// build, even the newest on GitHub, never leaves the back-office).
// Run: node --test lib/download.test.ts
import assert from 'node:assert/strict';
import { test } from 'node:test';

import { publishedApk } from './download.ts';

const hub = 'https://hub.example';
const ok = (body: unknown) => async () => new Response(JSON.stringify(body), { status: 200 });

test('the hub has a published build: its file, asked for the website channel', async () => {
  let asked = '';
  const apk = await publishedApk(hub, async (url) => {
    asked = String(url);
    return ok({ versionName: '0.1.8', build: 26, url: 'https://github.com/o/r/releases/download/mobile-v0.1.8/kergui-0.1.8-build.26.apk', size: 70_000_000 })();
  });
  assert.equal(asked, 'https://hub.example/releases/v1/latest?platform=android&channel=website');
  assert.deepEqual(apk, {
    name: 'kergui-0.1.8-build.26.apk',
    url: 'https://github.com/o/r/releases/download/mobile-v0.1.8/kergui-0.1.8-build.26.apk',
    size: 70_000_000,
  });
});

test('nothing published yet (404): no file, never a fallback to GitHub', async () => {
  const apk = await publishedApk(hub, async () => new Response('{}', { status: 404 }));
  assert.equal(apk, null);
});

test('the hub is down or answers garbage: no file', async () => {
  assert.equal(await publishedApk(hub, async () => { throw new Error('down'); }), null);
  assert.equal(await publishedApk(hub, async () => new Response('oops', { status: 500 })), null);
  assert.equal(await publishedApk(hub, ok({ build: 26 })), null);
});

test('only an https .apk is handed out', async () => {
  assert.equal(await publishedApk(hub, ok({ url: 'http://github.com/x.apk', size: 1 })), null);
  assert.equal(await publishedApk(hub, ok({ url: 'https://github.com/x/page', size: 1 })), null);
});
