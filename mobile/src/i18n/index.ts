import { ApiError } from '../core/client';
import { fr, type MessageKey } from './fr';

export function t(key: MessageKey, params: Record<string, string | number> = {}): string {
  return fr[key].replace(/\{(\w+)\}/g, (_, k: string) => String(params[k] ?? `{${k}}`));
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
