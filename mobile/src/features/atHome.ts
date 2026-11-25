import { core } from '../core/client';
import { useSession } from '../session/store';

// « Proprio prioritaire » (issue #94). The box keeps one admin session. At
// home the owner's phone talks to the box directly and its session wins: the
// household's relay sees it, yields and pauses (internal/agent/yield.go).
// Away, the phone goes through the relay, and a command it signs there takes
// the box back from whatever the owner left open.
//
// "At home" is the box itself answering, at the address this phone signed in
// to, with the certificate it pinned then: the box's identity, the same proof
// the relay accepts when it cannot read the Wi-Fi's name (Android wants the
// location permission for that, the owner's phone does not ask for it). Away,
// on mobile data or another Wi-Fi, the address does not answer, or answers
// with another certificate.

/** How long the box gets to answer before the phone counts as away. */
export const AT_HOME_TIMEOUT_MS = 2500;

const norm = (fp: string) => fp.replace(/[^0-9a-f]/gi, '').toLowerCase();

/**
 * Whether this phone is at home, on the box's own network. False without a
 * signed-in box, without a pinned certificate (nothing to recognise it by),
 * or when the box does not answer in time: away is the safe answer, it only
 * means going through the relay.
 */
export async function probeAtHome(timeoutMs = AT_HOME_TIMEOUT_MS): Promise<boolean> {
  const s = useSession.getState().session;
  if (s?.mode !== 'router' || !s.pin) return false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const late = new Promise<string>((resolve) => {
    timer = setTimeout(() => resolve(''), timeoutMs);
  });
  try {
    const got = await Promise.race([core.fingerprint(s.routerUrl), late]);
    return typeof got === 'string' && got !== '' && norm(got) === norm(s.pin);
  } catch {
    return false;
  } finally {
    if (timer) clearTimeout(timer);
  }
}
