import { cookies } from 'next/headers';
import { SiteFooter, SiteHeader } from '../../_components/chrome';
import { CLIENT_PHONE_COOKIE } from '../../../lib/session';
import { signOut } from '../actions';

export default async function CompteLayout({ children }: { children: React.ReactNode }) {
  const phone = (await cookies()).get(CLIENT_PHONE_COOKIE)?.value;
  return (
    <>
      <SiteHeader brandHref="/compte" sub="mon compte">
        <form action={signOut} className="cl-me">
          {phone ? <span className="cl-mono">{phone}</span> : null}
          <button type="submit" className="cl-btn ghost small">
            Déconnexion
          </button>
        </form>
      </SiteHeader>
      <main className="cl-wrap cl-main">{children}</main>
      <SiteFooter />
    </>
  );
}
