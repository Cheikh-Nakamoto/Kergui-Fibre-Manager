// eslint-disable-next-line @typescript-eslint/no-require-imports
const { addAbiFilters } = require('../../plugins/withAbiFilters');

// The shape of android/app/build.gradle as `expo prebuild` generates it.
const GRADLE = `android {
    defaultConfig {
        applicationId 'org.kergui.app'
        versionName "0.1.0"
    }
}`;

test('adds ndk.abiFilters driven by the kergui.abiFilters property', () => {
  const out: string = addAbiFilters(GRADLE);
  expect(out).toMatch(/defaultConfig \{\s+\/\/ kergui-abi-filters\s+if \(findProperty\('kergui\.abiFilters'\)\)/);
  expect(out).toContain("ndk { abiFilters(*findProperty('kergui.abiFilters').split(',').collect { it.trim() }) }");
  expect(out).toContain("applicationId 'org.kergui.app'");
});

test('is idempotent', () => {
  const once = addAbiFilters(GRADLE);
  expect(addAbiFilters(once)).toBe(once);
});

test('fails loudly when the template changes', () => {
  expect(() => addAbiFilters('android {}')).toThrow(/defaultConfig/);
});
