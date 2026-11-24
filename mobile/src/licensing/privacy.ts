// The privacy policy (#37, Play's User Data policy): the hub serves it at
// /licensing/v1/privacy. Réglages opens the build's hub's copy; the demo,
// or a build without a hub, opens the public hub's.

/** The public Kergui hub, whose policy the demo and a hub-less build open. */
export const PUBLIC_HUB_URL = 'https://hub.kergui.click';

export function privacyUrl({ hubUrl, demo }: { hubUrl: string; demo: boolean }): string {
  const base = !demo && hubUrl ? hubUrl.replace(/\/+$/, '') : PUBLIC_HUB_URL;
  return `${base}/licensing/v1/privacy`;
}
