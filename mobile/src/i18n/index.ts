import { ApiError } from '../core/client';
import { HUB_ERROR_CODES, HubError } from '../licensing/api';
import { fr, type MessageKey } from './fr';

// `{name}` inserts a parameter; `{name|one|many}` picks a word by the number
// in `name`. French uses the singular for 0 and 1 ("0 bloqué", "1 bloqué").
export function t(key: MessageKey, params: Record<string, string | number> = {}): string {
  return fr[key].replace(/\{(\w+)(?:\|([^|}]*)\|([^}]*))?\}/g, (_, k: string, one?: string, many?: string) => {
    const v = params[k];
    if (one !== undefined && many !== undefined) return Math.abs(Number(v)) < 2 ? one : many;
    return String(v ?? `{${k}}`);
  });
}

/** A user-facing sentence for any error coming out of the core. */
export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.pinMismatch) return t('error.pin');
    // A box that is not there and a box that never answers are different
    // problems, and so is a core that did not start: saying "erreur" with a
    // Go sentence under it helps nobody.
    if (e.coreUnreachable) return t('error.core');
    if (e.timedOut) return t('error.timeout');
    switch (e.status) {
      case 501:
        return t('error.notSupported');
      case 401:
        return t('error.auth');
      case 502:
        return t('error.unreachable');
      case 503:
        return t('error.busy');
    }
    return t('error.generic', { error: e.message });
  }
  return t('error.generic', { error: e instanceof Error ? e.message : String(e) });
}

const HUB_CODES: ReadonlySet<string> = new Set(HUB_ERROR_CODES);

/** "45 s", "2 min": a wait as the rate limit gives it. */
function wait(seconds: number | undefined): string {
  const s = Math.max(1, Math.ceil(seconds ?? 60));
  return s < 60 ? `${s} s` : `${Math.ceil(s / 60)} min`;
}

/** A user-facing sentence for an error from the licensing hub. */
export function hubErrorMessage(e: unknown): string {
  if (e instanceof HubError && HUB_CODES.has(e.code)) {
    return t(`hubError.${e.code}` as MessageKey, { wait: wait(e.retryAfter) });
  }
  return t('error.generic', { error: e instanceof Error ? e.message : String(e) });
}
