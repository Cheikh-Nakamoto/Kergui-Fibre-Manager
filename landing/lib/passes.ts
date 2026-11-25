// Shapes and helpers of the customer area, safe on both server and client.

// --- shapes of internal/hub/licensing/handler ------------------------------------

export type Subscription = {
  boxId: string;
  tier: string;
  effectiveTier: string;
  active: boolean;
  expiresAt: string;
  channel: 'prepaid' | 'play';
  /** A pass with no end (issue #98): expiresAt is 9999-12-31, nothing to buy. */
  lifetime?: boolean;
};

export type CustomerPayment = {
  id: string;
  provider: string;
  boxId?: string;
  pass: string;
  amountXof: number;
  status: string;
  createdAt: string;
  creditedAt?: string;
  waiting?: boolean;
};

export type Trial = {
  state: 'available' | 'pending' | 'running' | 'used';
  boxId?: string;
  days: number;
  requestedAt?: string;
  startedAt?: string;
  endsAt?: string;
};

export type Pass = { code: string; tier: 'basic' | 'max'; term: '1m' | '3m' | '12m'; days: number; priceXof: number };

export type Catalogue = { passes: Pass[]; trialDays: number };

export const termLabel: Record<string, string> = { '1m': '1 mois', '3m': '3 mois', '12m': '12 mois' };

/** « Max · 3 mois » for a pass code such as max_3m. */
export function passName(code: string): string {
  const [tier, term] = code.split('_');
  if (code === 'max_trial') return 'Essai Max · 7 jours';
  const t = tier === 'max' ? 'Max' : tier === 'basic' ? 'Basic' : tier;
  return term ? `${t} · ${termLabel[term] ?? term}` : t;
}

/**
 * The Max time a running Basic converts into on an upgrade, as the hub
 * computes it (domain.ConvertBasicToMax): 650/2000 of what is left, rounded
 * down to the hour.
 */
export function basicToMaxHours(expiresAt: string, now = Date.now()): number {
  const secs = Math.max(0, Math.floor((new Date(expiresAt).getTime() - now) / 1000));
  return Math.floor((secs * 650) / 2000 / 3600);
}

// --- complaints, as the customer sees them (internal/hub/complaints, issue #85) ---

export type ComplaintCategory = 'paiement' | 'box' | 'application' | 'autre';

/** In the order the form shows them. */
export const COMPLAINT_CATEGORIES: readonly ComplaintCategory[] = ['paiement', 'box', 'application', 'autre'];

/** A message of the thread, in characters (the hub's MaxMessage). */
export const COMPLAINT_MAX_MESSAGE = 2000;

/** One message of the thread: the customer's, or Kergui's answer. */
export type CustomerComplaintMessage = {
  id: number;
  from: 'customer' | 'admin';
  /** Where it was written: app, web, whatsapp, backoffice. */
  channel: string;
  body: string;
  at: string;
};

/** GET /licensing/v1/complaints: the customer's complaints, newest first, with their thread. */
export type CustomerComplaint = {
  id: string;
  number: number;
  source: 'app' | 'web' | 'whatsapp';
  category: ComplaintCategory;
  status: 'open' | 'answered' | 'closed';
  createdAt: string;
  updatedAt: string;
  closedAt?: string;
  hasJournal: boolean;
  messages?: CustomerComplaintMessage[];
};
