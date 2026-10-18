// Release builds take their numbers from CI (mobile-release.yml):
//   KERGUI_VERSION       the tag without "mobile-v", e.g. 1.2.0 (shown in the stores)
//   KERGUI_BUILD_NUMBER  github.run_number (versionCode / CFBundleVersion, must grow)
// Everything else lives in app.json.
module.exports = ({ config }) => {
  const version = process.env.KERGUI_VERSION || config.version;
  const build = Number(process.env.KERGUI_BUILD_NUMBER || 1);
  return {
    ...config,
    version,
    android: { ...config.android, versionCode: build },
    ios: { ...config.ios, buildNumber: String(build) },
  };
};
