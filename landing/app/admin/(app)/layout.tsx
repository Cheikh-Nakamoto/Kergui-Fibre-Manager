import Image from 'next/image';
import Link from 'next/link';
import { hub } from '../../../lib/hub';
import { logout } from '../actions';

const nav = [
  { href: '/admin', label: 'Santé' },
  { href: '/admin/support', label: 'Support' },
  { href: '/admin/foyers', label: 'Foyers' },
  { href: '/admin/passes', label: 'Passes' },
  { href: '/admin/paiements', label: 'Paiements' },
  { href: '/admin/versions', label: 'Versions' },
  { href: '/admin/whatsapp', label: 'WhatsApp' },
  { href: '/admin/journal', label: 'Journal' },
];

export default async function AdminLayout({ children }: { children: React.ReactNode }) {
  // Also the access check: the hub answers 403 to anyone but a platform admin.
  const me = await hub<{ email: string }>('/api/v1/admin/me');
  return (
    <>
      <header className="bo-top">
        <div className="bo-wrap bo-top-inner">
          <Link href="/admin" className="bo-brand">
            <Image src="/logo.png" alt="" width={28} height={28} />
            <span>Kergui</span>
            <small>back-office</small>
          </Link>
          <nav className="bo-nav" aria-label="Back-office">
            {nav.map((n) => (
              <Link key={n.href} href={n.href}>
                {n.label}
              </Link>
            ))}
          </nav>
          <form action={logout} className="bo-me">
            <span title={me.email}>{me.email}</span>
            <button type="submit" className="bo-btn ghost small">
              Déconnexion
            </button>
          </form>
        </div>
      </header>
      <main className="bo-wrap bo-main">{children}</main>
    </>
  );
}
