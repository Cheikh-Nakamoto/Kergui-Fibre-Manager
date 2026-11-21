// eslint-disable-next-line @typescript-eslint/no-require-imports
const { readLicensingConfig, applyLicensing } = require('../../config/licensing');
const appJson = require('../../app.json');

const KEY = 'ebVWLo/mVPlAeLES6KmLp5AfhTrmlb7X4OORC60ElmQ=';

describe('readLicensingConfig', () => {
  test('defaults to the play flavor, with no hub: a forgotten variable never ships NabooPay to Google Play', () => {
    expect(readLicensingConfig({})).toEqual({ flavor: 'play', hubUrl: '', publicKey: '' });
  });

  test('reads the flavor, the hub URL and the public key', () => {
    expect(
      readLicensingConfig({
        KERGUI_FLAVOR: ' NabooPay ',
        KERGUI_HUB_URL: 'https://hub.kergui.test/',
        KERGUI_LICENSING_PUBLIC_KEY: KEY,
      }),
    ).toEqual({ flavor: 'naboopay', hubUrl: 'https://hub.kergui.test', publicKey: KEY });
  });

  test('refuses an unknown flavor rather than guessing', () => {
    expect(() => readLicensingConfig({ KERGUI_FLAVOR: 'appstore' })).toThrow(/KERGUI_FLAVOR/);
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
    const out = applyLicensing({ ...base, extra: { other: 1 } }, { KERGUI_FLAVOR: 'play', KERGUI_LICENSING_PUBLIC_KEY: KEY });
    expect(out.extra).toEqual({ other: 1, licensing: { flavor: 'play', hubUrl: '', publicKey: KEY } });
    expect(out.android.package).toBe('org.kergui.app');
    expect(out.android.blockedPermissions).not.toContain('com.android.vending.BILLING');
  });

  test('the NabooPay APK does not ask for the Play billing permission', () => {
    const out = applyLicensing(base, { KERGUI_FLAVOR: 'naboopay' });
    expect(out.extra.licensing.flavor).toBe('naboopay');
    expect(out.android.blockedPermissions).toEqual([...base.android.blockedPermissions, 'com.android.vending.BILLING']);
    // Same application id: one app, two ways to pay.
    expect(out.android.package).toBe('org.kergui.app');
  });
});
