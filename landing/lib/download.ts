// The APK the website hands out: the one a platform administrator published
// in the back-office (« Versions »), as the hub answers it. A build CI has
// only declared (pending) or that was withdrawn never leaves this way, even
// when it is the newest release on GitHub. No fallback to GitHub: that was
// the bug — /telecharger served every fresh build before its review.

export type PublishedApk = { name: string; url: string; size: number };

type Fetch = (url: string, init?: RequestInit) => Promise<Response>;

export async function publishedApk(hub: string, get: Fetch = fetch): Promise<PublishedApk | null> {
  let res: Response;
  try {
    res = await get(`${hub}/releases/v1/latest?platform=android&channel=website`, {
      headers: { Accept: 'application/json' },
      cache: 'no-store',
    });
  } catch {
    return null;
  }
  if (!res.ok) return null;
  let body: { url?: unknown; size?: unknown };
  try {
    body = (await res.json()) as typeof body;
  } catch {
    return null;
  }
  const url = typeof body.url === 'string' ? body.url : '';
  if (!url.startsWith('https://') || !url.endsWith('.apk')) return null;
  const name = url.slice(url.lastIndexOf('/') + 1);
  return { name, url, size: typeof body.size === 'number' ? body.size : 0 };
}
