// eslint-disable-next-line @typescript-eslint/no-require-imports
const { readLicensingConfig, applyLicensing } = require('../../config/licensing');
const appJson = require('../../app.json');

const KEY = 'ebVWLo/mVPlAeLES6KmLp5AfhTrmlb7X4OORC60ElmQ=';

describe('readLicensingConfig', () => {
  test('one build: no flavor, and no hub unless one is given', () => {
    expect(readLicensingConfig({})).toEqual({ hubUrl: '', publicKey: '' });
    // A leftover KERGUI_FLAVOR changes nothing.
    expect(readLicensingConfig({ KERGUI_FLAVOR: 'naboopay' })).toEqual({ hubUrl: '', publicKey: '' });
  });

  test('reads the hub URL and the public key', () => {
    expect(readLicensingConfig({ KERGUI_HUB_URL: 'https://hub.kergui.test/', KERGUI_LICENSING_PUBLIC_KEY: KEY })).toEqual({
      hubUrl: 'https://hub.kergui.test',
      publicKey: KEY,
    });
  });

  test('the hub must be https, except a local hub in development', () => {
    expect(() => readLicensingConfig({ KERGUI_HUB_URL: 'http://hub.kergui.test' })).toThrow(/https/);
    expect(() => readLicensingConfig({ KERGUI_HUB_URL: 'hub.kergui.test' })).toThrow(/https/);
    expect(readLicensingConfig({ KERGUI_HUB_URL: 'http://10.0.2.2:8080' }).hubUrl).toBe('http://10.0.2.2:8080');
    expect(readLicensingConfig({ KERGUI_HUB_URL: 'http://localhost:8080' }).hubUrl).toBe('http://localhost:8080');
  });

  test('the public key must be a raw 32-byte Ed25519 key in base64', () => {
    expect(() => readLicensingConfig({ KERGUI_LICENSING_PUBLIC_KEY: 'AQID' })).toThrow(/32 bytes/);
    expect(() => readLicensingConfig({ KERGUI_LICENSING_PUBLIC_KEY: 'not base64!' })).toThrow(/32 bytes/);
  });
});

describe('applyLicensing', () => {
  const base = appJson.expo;

  test('exposes the licensing settings through extra, keeping what was there', () => {
    const out = applyLicensing({ ...base, extra: { other: 1 } }, { KERGUI_LICENSING_PUBLIC_KEY: KEY });
    expect(out.extra).toEqual({ other: 1, licensing: { hubUrl: '', publicKey: KEY } });
    expect(out.android.package).toBe('org.kergui.app');
  });

  test('the app never asks for the Play billing permission', () => {
    const out = applyLicensing(base, {});
    expect(out.android.blockedPermissions).toEqual([...base.android.blockedPermissions, 'com.android.vending.BILLING']);
    expect(out.android.permissions ?? []).not.toContain('com.android.vending.BILLING');
    // Applying it twice does not list it twice.
    expect(applyLicensing(out, {}).android.blockedPermissions.filter((p: string) => p.endsWith('BILLING'))).toHaveLength(1);
  });
});

test('no payment dependency in the app', () => {
  const pkg = require('../../package.json');
  const deps = Object.keys({ ...pkg.dependencies, ...pkg.devDependencies });
  expect(deps.filter((d) => /iap|billing|purchase|naboo/i.test(d))).toEqual([]);
});
