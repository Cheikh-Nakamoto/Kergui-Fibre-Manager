import { ApiError } from '../core/client';
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
