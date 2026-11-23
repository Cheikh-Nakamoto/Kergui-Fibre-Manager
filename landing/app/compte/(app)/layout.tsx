import Image from 'next/image';
import Link from 'next/link';
import { cookies } from 'next/headers';
import { CLIENT_PHONE_COOKIE } from '../../../lib/session';
import { signOut } from '../actions';

export default async function CompteLayout({ children }: { children: React.ReactNode }) {
  const phone = (await cookies()).get(CLIENT_PHONE_COOKIE)?.value;
  return (
    <>
      <header className="cl-top">
        <div className="cl-wrap cl-top-inner">
          <Link href="/compte" className="cl-brand">
            <Image src="/logo.png" alt="" width={28} height={28} />
            <span>Kergui</span>
            <small>mon compte</small>
          </Link>
          <form action={signOut} className="cl-me">
            {phone ? <span className="cl-mono">{phone}</span> : null}
            <button type="submit" className="cl-btn ghost small">
              Déconnexion
            </button>
          </form>
        </div>
      </header>
      <main className="cl-wrap cl-main">{children}</main>
    </>
  );
}
