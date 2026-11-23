import { site } from '../../lib/site';

// GET /telecharger: downloads the newest Android APK, from kergui.click.
//
// The APKs are GitHub release assets, published as pre-releases with the
// build number in their name, so neither "releases/latest" nor a fixed URL
// reaches them. This route finds the newest release that carries an .apk and
// streams the file itself, as an attachment from this site: the browser
// downloads it without ever showing a GitHub page (which, in an in-app
// browser tab, left the download stuck at 100 %).

const REPO = 'Cheikh-Nakamoto/Kergui-Fibre-Manager';

type Release = {
  draft: boolean;
  published_at: string | null;
  assets: { name: string; size: number; browser_download_url: string }[];
};

export const dynamic = 'force-dynamic';

async function newestApk() {
  const res = await fetch(`https://api.github.com/repos/${REPO}/releases?per_page=20`, {
    headers: { Accept: 'application/vnd.github+json', 'User-Agent': 'kergui-site' },
    // Five minutes of cache: well inside GitHub's anonymous rate limit.
    next: { revalidate: 300 },
  });
  if (!res.ok) return null;
  const releases = (await res.json()) as Release[];
  return (
    releases
      .filter((r) => !r.draft && r.published_at)
      .sort((a, b) => Date.parse(b.published_at!) - Date.parse(a.published_at!))
      .flatMap((r) => r.assets)
      .find((a) => a.name.endsWith('.apk')) ?? null
  );
}

function fallback() {
  return Response.redirect(`${site.sourceUrl}/releases`, 302);
}

export async function GET() {
  let apk;
  try {
    apk = await newestApk();
  } catch {
    return fallback();
  }
  if (!apk) return fallback();

  // Never cached by Next: the file is ~140 MB, streamed through as it comes.
  const file = await fetch(apk.browser_download_url, { cache: 'no-store', redirect: 'follow' }).catch(() => null);
  if (!file || !file.ok || !file.body) return Response.redirect(apk.browser_download_url, 302);

  const headers = new Headers({
    'Content-Type': 'application/vnd.android.package-archive',
    'Content-Disposition': `attachment; filename="${apk.name}"`,
    'Cache-Control': 'no-store',
    'X-Content-Type-Options': 'nosniff',
  });
  const length = file.headers.get('content-length') ?? String(apk.size);
  if (length) headers.set('Content-Length', length);
  return new Response(file.body, { status: 200, headers });
}
