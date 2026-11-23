'use client';

// A form opened before a redeploy calls a Server Action the new build may no
// longer have. Rather than leaving the button doing nothing, reload the page:
// the visitor gets the new build and submits again.
export function reloadOnStale<S>(action: (prev: S, form: FormData) => Promise<S>) {
  return async (prev: S, form: FormData): Promise<S> => {
    try {
      return await action(prev, form);
    } catch (e) {
      const digest = (e as { digest?: string } | null)?.digest ?? '';
      if (digest.startsWith('NEXT_REDIRECT') || digest.startsWith('NEXT_HTTP_ERROR')) throw e;
      window.location.reload();
      return prev;
    }
  };
}
