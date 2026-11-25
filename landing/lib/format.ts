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

/**
 * The end of a box's pass as the pages say it: « à vie » for a lifetime pass
 * (issue #98, its date is a placeholder 9999-12-31), else until or since when.
 */
export function passEnd(p: { expiresAt?: string; active?: boolean; lifetime?: boolean }): string {
  if (p.lifetime) return 'à vie';
  return `${p.active === false ? 'expiré le' : 'jusqu’au'} ${date(p.expiresAt)}`;
}

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
  ANNOUNCEMENT_SENT: 'Annonce envoyée',
  COMPLAINT_ANSWERED: 'Réponse à une plainte',
  COMPLAINT_CLOSED: 'Plainte close',
  COMPLAINT_ADMINS_SET: 'Numéros admin (plaintes) changés',
  PASS_RENEWED_BY_ADMIN: 'Pass renouvelé (à la main)',
  PASS_LIFETIME_GRANTED: 'Pass à vie accordé',
  PASS_LIFETIME_REVOKED: 'Pass à vie retiré',
  ACCOUNT_DELETED: 'Compte supprimé par son titulaire',
  RENEWAL_REMINDERS_SET: 'Rappels de renouvellement activés ou désactivés',
};

/** A renewal reminder's step (issue #99). */
export const reminderStepLabel: Record<string, string> = { j3: 'J-3', j0: 'Jour J' };

/** What a reminder's WhatsApp message did. */
export const reminderWhatsAppLabel: Record<string, string> = {
  sending: 'WhatsApp en cours',
  sent: 'WhatsApp envoyé',
  failed: 'WhatsApp en échec',
  skipped: 'Pas envoyé',
};

/** What a reminder's app notification did. */
export const reminderPushLabel: Record<string, string> = {
  sending: 'appli en cours',
  sent: 'appli envoyée',
  failed: 'appli en échec',
  none: 'pas de téléphone avec l’appli',
  off: 'notifications appli désactivées sur le hub',
  skipped: '',
};

/** A WhatsApp chat with the number (wa.me wants the digits only). */
export function whatsappLink(phone: string): string {
  return `https://wa.me/${phone.replace(/\D/g, '')}`;
}

/** "dans 3 j", "aujourd’hui", "il y a 5 j": an end date seen from now. */
export function daysFrom(iso: string, now: string): string {
  const ms = new Date(iso).getTime() - new Date(now).getTime();
  const days = Math.round(Math.abs(ms) / 86_400_000);
  if (days === 0) return ms >= 0 ? 'aujourd’hui' : 'aujourd’hui (terminé)';
  return ms >= 0 ? `dans ${days} j` : `il y a ${days} j`;
}

/** "67 %", or "—" without a rate. */
export function percent(rate?: number): string {
  return rate === undefined ? '—' : `${Math.round(rate * 100)} %`;
}

/** Who took the payment. */
export const providerLabel: Record<string, string> = {
  naboopay: 'NabooPay',
  play: 'Google Play',
  support: 'Geste commercial',
  manual: 'Paiement à la main',
};

export const targetLabel: Record<string, string> = {
  account: 'Compte',
  household: 'Foyer',
  box: 'Box',
  payment: 'Paiement',
  release: 'Version',
  message: 'Message',
  whatsapp: 'WhatsApp du hub',
  complaint: 'Plainte',
  settings: 'Réglages',
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

/** Who an announcement went to. */
export function audienceLabel(audience: string, value?: string, household?: { name: string; phone?: string }): string {
  if (audience === 'all') return 'Tous les foyers';
  if (audience === 'tier') return `Niveau ${tierLabel[value ?? ''] ?? value}`;
  if (audience === 'household') return `Foyer ${household?.phone || household?.name || value || ''}`.trim();
  if (audience === 'numbers') return 'Numéros choisis';
  return audience;
}

/** Why a number does not receive a WhatsApp message. */
export const refusedLabel: Record<string, string> = {
  invalid: 'numéro invalide',
  not_opted_in: 'WhatsApp non activé',
  stopped: 'a répondu STOP',
};

/** "≈ 3 min", "≈ 40 s": how long the WhatsApp queue takes. */
export function durationLabel(seconds: number): string {
  if (seconds < 60) return `≈ ${Math.round(seconds)} s`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `≈ ${minutes} min`;
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return `≈ ${h} h${m ? ` ${String(m).padStart(2, '0')}` : ''}`;
}

/** "3 foyers, 5 téléphones". */
export function reachLabel(households: number, phones: number): string {
  return `${households} foyer${households > 1 ? 's' : ''}, ${phones} téléphone${phones > 1 ? 's' : ''}`;
}

// --- complaints (issue #82) -----------------------------------------------------

export const complaintStatusLabel: Record<string, string> = {
  open: 'Ouverte',
  answered: 'Répondue',
  closed: 'Close',
};

/** The pill's tone of a complaint's status. */
export const complaintStatusTone: Record<string, string> = {
  open: 'warn',
  answered: 'ok',
  closed: 'off',
};

export const complaintCategoryLabel: Record<string, string> = {
  paiement: 'Paiement',
  box: 'Box / connexion',
  application: 'Application',
  autre: 'Autre',
};

export const complaintSourceLabel: Record<string, string> = {
  app: 'Application',
  web: 'Site (espace client)',
  whatsapp: 'WhatsApp',
  backoffice: 'Back-office',
};

/** What became of an answer's WhatsApp copy. */
export const complaintWhatsAppLabel: Record<string, string> = {
  queued: 'WhatsApp en file',
  sent: 'WhatsApp envoyé',
  failed: 'WhatsApp en échec',
  not_opted_in: 'pas de WhatsApp (non activé)',
  off: 'WhatsApp non configuré',
};

/** "12,4 Ko", for a journal's size. */
export function ko(bytes: number): string {
  return `${(bytes / 1024).toLocaleString('fr-FR', { maximumFractionDigits: 1 })} Ko`;
}
