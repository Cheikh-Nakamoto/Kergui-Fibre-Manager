// eslint-disable-next-line @typescript-eslint/no-require-imports
const { addSelfUpdate, MANIFEST } = require('../../plugins/withSelfUpdate');

// The website APK only (#74): the install permission, the flag and the
// FileProvider come with -Pkergui.selfUpdate=true; the Play bundle has none.
const GRADLE = `android {
    defaultConfig {
        applicationId 'org.kergui.app'
    }
}`;

test('the release manifest overlay is switched on by the kergui.selfUpdate property only', () => {
  const out: string = addSelfUpdate(GRADLE);
  expect(out.startsWith(GRADLE)).toBe(true);
  expect(out).toContain("if (findProperty('kergui.selfUpdate')?.toString() == 'true') {");
  expect(out).toMatch(/sourceSets \{\s+release \{\s+manifest\.srcFile 'src\/selfUpdate\/AndroidManifest\.xml'/);
});

test('the overlay carries the permission, the flag and the FileProvider', () => {
  expect(MANIFEST).toContain('android.permission.REQUEST_INSTALL_PACKAGES');
  expect(MANIFEST).toContain('android:name="org.kergui.selfUpdate" android:value="true"');
  expect(MANIFEST).toContain('android:name="org.kergui.core.UpdateFileProvider"');
  expect(MANIFEST).toContain('android:authorities="${applicationId}.kergui.update"');
  expect(MANIFEST).toContain('@xml/kergui_update_paths');
});

test('is idempotent', () => {
  const once = addSelfUpdate(GRADLE);
  expect(addSelfUpdate(once)).toBe(once);
});

test('fails loudly when the template changes', () => {
  expect(() => addSelfUpdate('plugins {}')).toThrow(/android block/);
});
