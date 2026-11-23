import { site } from '../../lib/site';

// GET /telecharger: starts the download of the newest Android APK.
//
// The APKs are GitHub release assets, published as pre-releases with the
// build number in their name, so neither "releases/latest" nor a fixed URL
// reaches them. This route finds the newest release that carries an .apk and
// redirects straight to the file: GitHub serves it as an attachment, so the
// browser downloads it and the visitor stays on the page.

const REPO = 'Cheikh-Nakamoto/Kergui-Fibre-Manager';

type Release = {
  draft: boolean;
  published_at: string | null;
  assets: { name: string; browser_download_url: string }[];
};

export const dynamic = 'force-dynamic';

export async function GET() {
  try {
    const res = await fetch(`https://api.github.com/repos/${REPO}/releases?per_page=20`, {
      headers: { Accept: 'application/vnd.github+json', 'User-Agent': 'kergui-site' },
      // Five minutes of cache: well inside GitHub's anonymous rate limit.
      next: { revalidate: 300 },
    });
    if (res.ok) {
      const releases = (await res.json()) as Release[];
      const apk = releases
        .filter((r) => !r.draft && r.published_at)
        .sort((a, b) => Date.parse(b.published_at!) - Date.parse(a.published_at!))
        .flatMap((r) => r.assets)
        .find((a) => a.name.endsWith('.apk'));
      if (apk) return Response.redirect(apk.browser_download_url, 302);
    }
  } catch {
    // Fall through to the releases page.
  }
  return Response.redirect(`${site.sourceUrl}/releases`, 302);
}
