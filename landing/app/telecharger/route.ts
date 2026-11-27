import { publishedApk } from '../../lib/download';
import { hubURL } from '../../lib/session';

// GET /telecharger: downloads the Android APK published in the back-office
// (« Versions »), from kergui.click.
//
// The hub says which build is published (GET /releases/v1/latest): a build
// CI only declared, or one that was withdrawn, is never handed out, even when
// it is the newest release on GitHub. This route streams the file itself, as
// an attachment from this site: the browser downloads it without ever
// showing a GitHub page (which, in an in-app browser tab, left the download
// stuck at 100 %).

export const dynamic = 'force-dynamic';

// No published build yet, or the hub cannot be reached: say so, never hand
// out an unreviewed build instead.
function notYet() {
  const html = `<!doctype html><html lang="fr"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>Kergui</title></head>
<body style="font-family:system-ui,sans-serif;background:#07140f;color:#e8f2ed;margin:0;padding:48px 24px;line-height:1.6">
<main style="max-width:32rem;margin:0 auto"><h1 style="font-size:1.4rem">Le téléchargement n’est pas disponible pour le moment.</h1>
<p>Réessayez dans quelques minutes.</p><p><a href="/" style="color:#6fd8ae">Retour à l’accueil</a></p></main></body></html>`;
  return new Response(html, {
    status: 503,
    headers: { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store', 'Retry-After': '300' },
  });
}

export async function GET() {
  const apk = await publishedApk(hubURL());
  if (!apk) return notYet();

  // Never cached by Next: the file is ~140 MB, streamed through as it comes.
  const file = await fetch(apk.url, { cache: 'no-store', redirect: 'follow' }).catch(() => null);
  if (!file || !file.ok || !file.body) return Response.redirect(apk.url, 302);

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
