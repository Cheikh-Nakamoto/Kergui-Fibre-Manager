// eslint-disable-next-line @typescript-eslint/no-require-imports
const { addSigning } = require('../../plugins/withReleaseSigning');

// The shape of android/app/build.gradle as `expo prebuild` generates it.
const GRADLE = `android {
    signingConfigs {
        debug {
            storeFile file('debug.keystore')
        }
    }
    buildTypes {
        debug {
            signingConfig signingConfigs.debug
        }
        release {
            signingConfig signingConfigs.debug
            minifyEnabled enableMinifyInReleaseBuilds
        }
    }
}`;

test('adds a release signing config read from the environment', () => {
  const out: string = addSigning(GRADLE);
  expect(out).toContain('storeFile file(System.getenv("KERGUI_UPLOAD_STORE_FILE"))');
  expect(out).toMatch(/release \{\s+signingConfig System\.getenv\("KERGUI_UPLOAD_STORE_FILE"\) \? signingConfigs\.release : signingConfigs\.debug/);
  // The debug build type keeps the debug key.
  expect(out).toMatch(/debug \{\s+signingConfig signingConfigs\.debug/);
});

test('is idempotent', () => {
  const once = addSigning(GRADLE);
  expect(addSigning(once)).toBe(once);
});

test('fails loudly when the template changes', () => {
  expect(() => addSigning('android {}')).toThrow(/signingConfigs/);
});
