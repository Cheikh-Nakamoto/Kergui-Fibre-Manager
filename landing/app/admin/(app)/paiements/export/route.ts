import { cookies } from 'next/headers';
import { NextResponse, type NextRequest } from 'next/server';
import { ACCESS_COOKIE, hubURL } from '../../../../../lib/session';

// The CSV export, proxied: the browser downloads from the back-office, which
// calls the hub with the admin's token from its httpOnly cookie, so the token
// never sits in a URL. The hub's stream is passed through as it comes.

const DAY = /^\d{4}-\d{2}-\d{2}$/;

export async function GET(request: NextRequest) {
  const token = (await cookies()).get(ACCESS_COOKIE)?.value;
  if (!token) return NextResponse.redirect(new URL('/admin/login', request.url));

  const q = new URLSearchParams();
  for (const key of ['from', 'to']) {
    const v = request.nextUrl.searchParams.get(key) ?? '';
    if (DAY.test(v)) q.set(key, v);
  }
  const back = (error: string) => {
    const url = new URL('/admin/paiements', request.url);
    url.searchParams.set('exportError', error);
    return NextResponse.redirect(url, 303);
  };

  let res: Response;
  try {
    res = await fetch(`${hubURL()}/api/v1/admin/payments/export.csv?${q}`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: 'no-store',
    });
  } catch {
    return back('Le serveur Kergui ne répond pas.');
  }
  if (res.status === 401) return NextResponse.redirect(new URL('/admin/login', request.url));
  if (!res.ok || !res.body) {
    const body = (await res.json().catch(() => ({}))) as { error?: { message?: string } };
    const m = body.error?.message ?? '';
    if (m.includes('before')) return back('La date de début doit précéder la date de fin.');
    if (m.includes('too long')) return back('Période trop longue : deux ans au plus.');
    return back(m || `Le serveur Kergui a répondu ${res.status}.`);
  }
  return new Response(res.body, {
    headers: {
      'Content-Type': 'text/csv; charset=utf-8',
      'Content-Disposition': res.headers.get('Content-Disposition') ?? 'attachment; filename="kergui-paiements.csv"',
      'Cache-Control': 'no-store',
    },
  });
}
