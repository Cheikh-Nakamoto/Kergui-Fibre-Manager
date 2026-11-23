import { hub, HubError, type WhatsAppStatus } from '../../../../lib/hub';
import { LinkForm, UnlinkButton } from './forms';

export const metadata = { title: 'WhatsApp' };

export default async function WhatsAppPage() {
  let st: WhatsAppStatus | null = null;
  try {
    st = await hub<WhatsAppStatus>('/api/v1/admin/whatsapp');
  } catch (e) {
    // No whatsmeow on this hub (console provider): the routes do not exist.
    if (!(e instanceof HubError && e.status === 404)) throw e;
  }
  return (
    <>
      <div className="ad-head">
        <h1>Session WhatsApp</h1>
        <p className="ad-muted">
          Le hub envoie et reçoit WhatsApp lui-même, comme un « appareil connecté » du numéro Kergui.
        </p>
      </div>

      {!st ? (
        <p className="ad-banner warn">
          Ce hub n’utilise pas WhatsApp : les messages vont dans son journal. Réglez <code>KERGUI_HUB_NOTIFIER=whatsmeow</code> et{' '}
          <code>KERGUI_HUB_WHATSAPP_NUMBER</code>, puis redéployez.
        </p>
      ) : (
        <section className="ad-card">
          <div className="ad-stats">
            <div className={`ad-stat ${st.connected ? 'ok' : 'bad'}`}>
              <span>État</span>
              <strong>{st.connected ? 'Connecté' : st.linked ? 'Déconnecté' : 'Non lié'}</strong>
            </div>
            <div className="ad-stat">
              <span>Numéro lié</span>
              <strong className="ad-mono">{st.number ?? '—'}</strong>
            </div>
          </div>

          {st.linked ? (
            <>
              <p className="ad-muted">
                Pour changer de numéro, déliez d’abord celui-ci. Les foyers ne recevront plus de message WhatsApp tant
                qu’un autre numéro n’est pas lié (les notifications continuent).
              </p>
              <UnlinkButton />
            </>
          ) : (
            <>
              <ol className="ad-steps">
                <li>Demandez un code ci-dessous (le numéro configuré sur le hub est utilisé si vous n’en saisissez pas).</li>
                <li>
                  Sur le téléphone du numéro Kergui, ouvrez WhatsApp → <strong>Appareils connectés</strong> →{' '}
                  <strong>Connecter un appareil</strong> → <strong>Connecter avec un numéro de téléphone</strong>.
                </li>
                <li>Saisissez le code. Il expire au bout de quelques minutes.</li>
                <li>Rechargez cette page : l’état passe à « Connecté ».</li>
              </ol>
              <LinkForm />
            </>
          )}
        </section>
      )}
    </>
  );
}
