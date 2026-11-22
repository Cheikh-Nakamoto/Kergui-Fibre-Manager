// eslint-disable-next-line @typescript-eslint/no-require-imports
const { readPushConfig, applyPush } = require('../../config/push');

const ID = '0f8b1c2d-3e4f-4a5b-8c6d-7e8f9a0b1c2d';

describe('push build settings', () => {
  test('off without an EAS project', () => {
    expect(readPushConfig({})).toEqual({ projectId: '', googleServicesFile: '' });
    const config = { name: 'Kergui', extra: { licensing: {} }, android: { package: 'org.kergui.app' } };
    expect(applyPush(config, {})).toEqual(config);
  });

  test('the project id and the Firebase file reach the config', () => {
    const out = applyPush(
      { extra: { licensing: { hubUrl: '' } }, android: { package: 'org.kergui.app' } },
      { KERGUI_EAS_PROJECT_ID: ` ${ID} `, KERGUI_GOOGLE_SERVICES_FILE: './google-services.json' },
    );
    expect(out.extra).toEqual({ licensing: { hubUrl: '' }, eas: { projectId: ID } });
    expect(out.android).toEqual({ package: 'org.kergui.app', googleServicesFile: './google-services.json' });
  });

  test('a project id that is not a UUID is refused', () => {
    expect(() => readPushConfig({ KERGUI_EAS_PROJECT_ID: 'kergui' })).toThrow(/UUID/);
  });
});
