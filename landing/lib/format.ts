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
