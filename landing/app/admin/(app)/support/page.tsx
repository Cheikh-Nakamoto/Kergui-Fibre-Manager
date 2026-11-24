import { redirect } from 'next/navigation';
import { hub, HubError, type SupportLookup } from '../../../../lib/hub';

export const metadata = { title: 'Support' };

/** A customer calls: their number, typed any way, opens their page. */
export default async function SupportPage({ searchParams }: { searchParams: Promise<{ phone?: string }> }) {
  const { phone = '' } = await searchParams;
  let found: SupportLookup | null = null;
  let error = '';
  if (phone.trim()) {
    try {
      found = await hub<SupportLookup>(`/api/v1/admin/support/lookup?phone=${encodeURIComponent(phone)}`);
    } catch (e) {
      if (!(e instanceof HubError)) throw e;
      error =
        e.status === 404
          ? 'Aucun compte pour ce numéro : il ne s’est jamais connecté ni n’a rien acheté.'
          : e.status === 400
            ? 'Ce n’est pas un numéro de téléphone. Exemples : 77 123 45 67, +221 77 123 45 67, 00221771234567.'
            : e.message;
    }
  }
  if (found) redirect(`/admin/support/${found.accountId}`);

  return (
    <>
      <div className="bo-head">
        <h1>Support</h1>
        <p className="bo-muted">
          Le numéro du client, sous n’importe quelle forme. Aucun code de connexion ne lui est envoyé.
        </p>
      </div>
      <section className="bo-card">
        <form className="bo-search" role="search">
          <input
            name="phone"
            defaultValue={phone}
            placeholder="77 123 45 67"
            aria-label="Numéro du client"
            inputMode="tel"
            autoFocus
            required
          />
          <button className="bo-btn" type="submit">
            Ouvrir la fiche
          </button>
        </form>
        {error ? (
          <p className="bo-error" role="alert">
            {error}
          </p>
        ) : null}
      </section>
    </>
  );
}
