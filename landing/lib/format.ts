const TZ = 'Africa/Dakar';

export function date(iso?: string | null): string {
  if (!iso) return '—';
  return new Date(iso).toLocaleDateString('fr-FR', { timeZone: TZ, day: '2-digit', month: 'short', year: 'numeric' });
}

export function dateTime(iso?: string | null): string {
  if (!iso) return '—';
  return new Date(iso).toLocaleString('fr-FR', {
    timeZone: TZ,
    day: '2-digit',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
  });
}

/** "il y a 3 min", for a last-seen time. */
export function ago(iso?: string | null): string {
  if (!iso) return 'jamais';
  const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 60) return `il y a ${s} s`;
  if (s < 3600) return `il y a ${Math.round(s / 60)} min`;
  if (s < 86400) return `il y a ${Math.round(s / 3600)} h`;
  return `il y a ${Math.round(s / 86400)} j`;
}

export function fcfa(n: number): string {
  return `${n.toLocaleString('fr-FR').replace(/ /g, ' ')} F`;
}

export const tierLabel: Record<string, string> = { free: 'Gratuit', basic: 'Basic', max: 'Max' };

export const roleLabel: Record<string, string> = {
  ROLE_OWNER: 'Propriétaire',
  ROLE_ADMIN: 'Administrateur',
  ROLE_AGENT: 'Agent',
  ROLE_VIEWER: 'Membre',
};

export const statusLabel: Record<string, string> = {
  pending: 'En attente',
  credited: 'Payé',
  failed: 'Échoué',
  cancelled: 'Annulé',
};

/** A file size in megabytes, "12,4 Mo". */
export function mo(bytes: number): string {
  return `${(bytes / 1_000_000).toLocaleString('fr-FR', { maximumFractionDigits: 1 })} Mo`;
}

export const releaseStateLabel: Record<string, string> = {
  pending: 'En attente',
  published: 'Publiée',
  withdrawn: 'Retirée',
};

/** The action log's gesture kinds, as a person reads them. */
export const actionLabel: Record<string, string> = {
  PASS_GRANTED_BY_SUPPORT: 'Pass offert',
  RELEASE_PUBLISHED: 'Version publiée',
  RELEASE_WITHDRAWN: 'Version retirée',
  RELEASE_NOTES_EDITED: 'Notes de version modifiées',
  RELEASE_MINIMUM_SET: 'Version minimale changée',
  WHATSAPP_SESSION_LINK_REQUESTED: 'Liaison WhatsApp demandée',
  WHATSAPP_SESSION_UNLINKED: 'WhatsApp délié',
  PASS_EXTENDED: 'Pass prolongé',
  PASS_TRANSFERRED_BY_ADMIN: 'Pass déplacé (support)',
  SESSIONS_REVOKED: 'Sessions révoquées',
  PAYMENT_RECHECKED: 'Paiement revérifié',
};

/** Who took the payment. */
export const providerLabel: Record<string, string> = {
  naboopay: 'NabooPay',
  play: 'Google Play',
  support: 'Geste commercial',
};

export const targetLabel: Record<string, string> = {
  account: 'Compte',
  household: 'Foyer',
  box: 'Box',
  payment: 'Paiement',
  release: 'Version',
  message: 'Message',
  whatsapp: 'WhatsApp du hub',
};

/** Where a customer's 7-day Max trial stands. */
export const trialStateLabel: Record<string, string> = {
  available: 'Disponible',
  pending: 'Demandé, en attente de box',
  running: 'En cours',
  used: 'Utilisé',
};

/** The household history's sources. */
export const historyKindLabel: Record<string, string> = {
  alert: 'Alerte',
  delivery: 'Envoi',
  relay: 'Relais',
  heartbeat: 'Signal',
  lease: 'Bail',
  approval: 'Approbation',
  payment: 'Paiement',
  admin: 'Back-office',
};
