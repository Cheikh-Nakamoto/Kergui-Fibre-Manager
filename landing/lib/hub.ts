import 'server-only';
import { cookies } from 'next/headers';
import { redirect } from 'next/navigation';
import { ACCESS_COOKIE, hubURL } from './session';

export class HubError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

/** Calls the hub as the signed-in administrator. 401 sends back to sign-in. */
export async function hub<T>(path: string, init: RequestInit = {}): Promise<T> {
  const token = (await cookies()).get(ACCESS_COOKIE)?.value;
  if (!token) redirect('/admin/login');
  const res = await fetch(`${hubURL()}${path}`, {
    ...init,
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}`, ...(init.headers ?? {}) },
    cache: 'no-store',
  });
  if (res.status === 401) redirect('/admin/login');
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = (body as { error?: { code?: string; message?: string } }).error ?? {};
    throw new HubError(res.status, err.code ?? 'ERROR', err.message ?? `hub answered ${res.status}`);
  }
  return body as T;
}

// --- shapes of internal/hub/admin and the whatsmeow admin routes ---------------

export type Health = {
  version: string;
  database: string;
  now: string;
  counts: {
    households: number;
    relays: number;
    relaysOnline: number;
    alerts24h: number;
    deliveries24h: number;
    failed24h: number;
    pendingPayments: number;
    activePasses: number;
  };
  recentFailures: { at: string; tenantId: string; channel: string; provider: string; error: string }[];
  whatsapp?: WhatsAppStatus;
};

export type Household = {
  tenantId: string;
  name: string;
  phone?: string;
  createdAt: string;
  members: number;
  relays: number;
  relaysOnline: number;
  devices: number;
  lastSnapshot?: string;
  tier: string;
  passUntil?: string;
  /** The running pass has no end (issue #98): passUntil is 9999-12-31, say « à vie ». */
  lifetime?: boolean;
};

/** One box's pass of an account; lifetime has no end (expiresAt 9999-12-31). */
export type BoxPass = {
  boxId: string;
  tier: string;
  expiresAt: string;
  active: boolean;
  channel?: string;
  lifetime?: boolean;
};

export type HouseholdDetail = {
  household: Household;
  members: { userId: string; email: string; role: string; status: string; whatsapp?: string; joinedAt?: string }[];
  relays: {
    id: string;
    name: string;
    kind: string;
    version?: string;
    seenAt?: string;
    online: boolean;
    revoked: boolean;
    eligible: boolean;
    reason?: string;
  }[];
  alerts: { at: string; devices: { no: number; mac: string; hostname?: string; ip?: string }[] }[];
  /** The account that owns the household; absent when signed up by e-mail (no pass). */
  accountId?: string;
  /** That account's passes, one per box: what the page's pass gestures act on (issue #98). */
  passes?: BoxPass[];
  /** The longest renewal in days. */
  maxDays?: number;
};

/** POST /api/v1/admin/households/{id}/passes/… — who the push reached. */
export type Notified = { phones: number; sent: number; failed: number; off?: boolean; error?: string };

export type Account = {
  accountId: string;
  phone: string;
  tenantId?: string;
  createdAt: string;
  subscriptions: BoxPass[];
  trial?: { boxId?: string; requestedAt: string; startedAt?: string };
};

export type Payment = {
  id: string;
  provider: string;
  reference: string;
  accountId: string;
  phone: string;
  boxId?: string;
  passCode: string;
  amountXof: number;
  status: string;
  createdAt: string;
  creditedAt?: string;
};

export type Passes = {
  accounts: Account[];
  payments: Payment[];
  catalogue: { code: string; tier: string; days: number; priceXof: number }[];
};

// --- expiring passes and renewals (issue #97) ------------------------------------

/** One pass (an account's subscription on a box) that ends soon, ended lately, or was lost. */
export type ExpiringPass = {
  accountId: string;
  phone: string;
  tenantId?: string;
  household?: string;
  boxId: string;
  tier: string;
  expiresAt: string;
  expired: boolean;
  /** The 7-day Max trial ending, not a paid pass. */
  trial: boolean;
  /** prepaid or play: a Play subscription renews by itself. */
  channel: string;
  /** The latest pass credited to the number, a gift included. */
  lastPayment?: { at: string; passCode: string; tier: string; amountXof: number; provider: string };
  /** The renewal reminders of this end date (issue #99), J-3 then J0. Absent from a hub before #99. */
  reminders?: ReminderSent[];
};

/** One renewal reminder of a pass's end date: when, and what each channel did. */
export type ReminderSent = {
  /** j3: three days before the end; j0: the day it ends. */
  step: 'j3' | 'j0';
  sentAt: string;
  /** sending, sent, failed (tried whatsappTries times) or skipped. */
  whatsapp: string;
  whatsappTries: number;
  /** The app notification: sending, sent, failed, none (no phone with the app), off (no push) or skipped. */
  push: string;
  pushPhones: number;
  pushSent: number;
  /** Why the reminder was skipped (a converted trial, a pass waiting for its box). */
  note?: string;
};

/** GET|PUT /api/v1/admin/settings/renewal-reminders — the reminders' switch and the hours (Dakar) they go out. */
export type RemindersSetting = { enabled: boolean; fromHour: number; toHour: number; updatedAt?: string };

/** GET /api/v1/admin/passes/expiring?status=soon|expired&tier=basic|max&q= — soonest end first. */
export type Expiring = {
  now: string;
  from: string;
  to: string;
  aheadDays: number;
  behindDays: number;
  passes: ExpiringPass[];
  /** The renewal reminders' switch (issue #99). Absent from a hub before #99. */
  reminders?: RemindersSetting;
};

/** GET /api/v1/admin/passes/renewals?month=YYYY-MM — the passes whose end date falls in the month. */
export type Renewals = {
  month: string;
  from: string;
  to: string;
  now: string;
  /** When the hub began recording renewals; earlier months are incomplete. */
  trackedSince?: string;
  /** due = renewed + lost; rate = renewed / due, absent when nothing came due. */
  renewal: { due: number; renewed: number; lost: number; upcoming: number; rate?: number };
  /** Passes running now; max includes the trials running (maxTrial). */
  active: { basic: number; max: number; maxTrial: number; lifetime?: number };
  /** Trials whose 7 days ended in the month, and those whose number then paid. */
  trials: { ended: number; converted: number };
  trialsAllTime: { started: number; converted: number };
  /** The month's lost passes: ended, not renewed. */
  lost: ExpiringPass[];
};

export type WhatsAppStatus = { linked: boolean; number?: string; connected: boolean };

export type ReleaseState = 'pending' | 'published' | 'withdrawn';

export type Release = {
  id: string;
  platform: string;
  channel: string;
  versionName: string;
  build: number;
  url: string;
  sha256: string;
  size: number;
  notes: string;
  state: ReleaseState;
  declaredAt: string;
  publishedAt?: string;
  withdrawnAt?: string;
  installs: number;
};

export type Releases = {
  platform: string;
  channel: string;
  minimumBuild: number;
  windowDays: number;
  releases: Release[];
  /** Phones seen per build over the window, builds never declared included. */
  versions: { build: number; installs: number }[];
};

/** One admin gesture of the action log (GET /api/v1/admin/audit). */
export type LogEntry = {
  id: string;
  at: string;
  admin: { id: string; email: string };
  kind: string;
  target: { type: string; id?: string };
  tenantId?: string;
  reason?: string;
  summary: string;
};

export type ActionLog = {
  entries: LogEntry[];
  /** Cursor of the next, older page; absent on the last one. */
  next?: string;
  /** The platform administrators and the gesture kinds, for the filters. */
  admins: { id: string; email: string }[];
  kinds: string[];
};

// --- support (issue #78) ------------------------------------------------------

/** GET /api/v1/admin/support/lookup?phone= */
export type SupportLookup = { accountId: string; phone: string };

/** A customer as support sees them (GET /api/v1/admin/support/accounts/{id}). */
export type Customer = {
  account: { id: string; phone: string; createdAt: string };
  /** The household the number owns; null until it signs in to the app. */
  household: Household | null;
  members: HouseholdDetail['members'];
  subscriptions: BoxPass[];
  waitingPasses: { paymentId: string; passCode: string; provider: string; amountXof: number; creditedAt?: string }[];
  trial: { state: string; boxId?: string; requestedAt?: string; startedAt?: string; endsAt?: string };
  payments: Payment[];
  sessions: { active: number; devices: { device: string; createdAt: string }[] };
  /** The customer's own transfers in the window; support's do not count. */
  transfers: { used: number; max: number; windowDays: number };
  maxExtendDays: number;
};

/** One hub-side trace of a household (GET /api/v1/admin/households/{id}/history). */
export type HistoryEvent = {
  id: string;
  at: string;
  kind: 'alert' | 'delivery' | 'relay' | 'heartbeat' | 'lease' | 'approval' | 'payment' | 'admin' | string;
  summary: string;
  detail?: string;
  failed?: boolean;
};

export type History = { events: HistoryEvent[]; next?: string };

// --- payments (issue #79) -----------------------------------------------------

/** An amount in F CFA and the number of payments behind it. */
export type Figures = { amountXof: number; count: number };
export type PassFigures = Figures & { pass: string; tier: string };
export type TierFigures = Figures & { tier: string };

/** Credited payments, gifts left out (GET /api/v1/admin/payments/revenue). */
export type Revenue = {
  from: string;
  to: string;
  by: 'day' | 'month';
  totals: Figures;
  /** Only the periods with payments, oldest first: "2026-11-03" or "2026-11". */
  periods: (Figures & { period: string; byPass: PassFigures[]; byTier: TierFigures[] })[];
  byPass: PassFigures[];
  byTier: TierFigures[];
};

export type WatchedPayment = Payment & {
  tier: string;
  tenantId?: string;
  /** A NabooPay order still pending: « Revérifier » can settle it. */
  recheckable: boolean;
};

/** Pending for too long, and failed lately (GET /api/v1/admin/payments/stuck). */
export type Stuck = {
  olderThanMinutes: number;
  failedDays: number;
  pending: WatchedPayment[];
  failed: WatchedPayment[];
  /** False when the hub has no NabooPay to ask. */
  recheck: boolean;
};

/** POST /api/v1/admin/payments/{id}/recheck */
export type Recheck = {
  paymentId: string;
  reference: string;
  previousStatus: string;
  status: string;
  credited: boolean;
  creditedAt?: string;
};

// --- messages (issue #80) -------------------------------------------------------

/** all, one level (free | basic | max), one household (its id), or hand-picked numbers (WhatsApp). */
export type MessageAudience = 'all' | 'tier' | 'household' | 'numbers';

export type MessageChannel = 'push' | 'whatsapp';

/** Why a number does not receive a WhatsApp message. */
export type RefusedReason = 'invalid' | 'not_opted_in' | 'stopped';

export type MessageHousehold = { tenantId: string; name: string; phone?: string };

/** Who an announcement reaches (POST /api/v1/admin/messages/preview). */
export type MessagePreview = {
  channel: MessageChannel;
  audience: MessageAudience;
  value?: string;
  household?: MessageHousehold;
  households: number;
  /** Households with at least one phone that registered for notifications. */
  householdsWithApp: number;
  phones: number;
  /** WhatsApp only: the numbers it goes to (E.164), those refused, and the queue's pace. */
  recipients?: string[];
  refused?: { number: string; reason: RefusedReason }[];
  intervalSeconds?: number;
  /** Messages the queue sends before this one. */
  ahead?: number;
  estimatedSeconds?: number;
  groupMax?: number;
};

/** POST /api/v1/admin/messages */
export type MessageSent = MessagePreview & { messageId: string; sent: number; failed: number; queued: number };

/** One announcement of the history (GET /api/v1/admin/messages). */
export type MessageEntry = {
  id: string;
  title: string;
  body: string;
  channel: 'push' | 'whatsapp' | string;
  audience: MessageAudience;
  value?: string;
  household?: MessageHousehold;
  author: { id?: string; email?: string };
  createdAt: string;
  households: number;
  phones: number;
  sent: number;
  failed: number;
  /** What the WhatsApp queue has yet to send. */
  queued: number;
};

export type Messages = {
  messages: MessageEntry[];
  /** False when the hub cannot push (no sender wired). */
  push: boolean;
  limits: { title: number; body: number };
  whatsapp: { enabled: boolean; intervalSeconds: number; groupMax: number; pending: number };
};

// --- complaints (issue #82) -----------------------------------------------------

export type ComplaintSource = 'app' | 'web' | 'whatsapp';
export type ComplaintCategory = 'paiement' | 'box' | 'application' | 'autre';
export type ComplaintStatus = 'open' | 'answered' | 'closed';

/** One message of a complaint's thread. */
export type ComplaintMessage = {
  id: number;
  from: 'customer' | 'admin';
  /** Where it was written: app, web, whatsapp, or backoffice for an answer. */
  channel: string;
  body: string;
  at: string;
  /** An answer only: who wrote it and what reached the customer. */
  author?: { id?: string; email?: string };
  pushed?: number;
  pushFailed?: number;
  /** The WhatsApp copy: queued, sent, failed, not_opted_in, off. */
  whatsapp?: string;
};

export type Complaint = {
  id: string;
  /** What the customer and the team say: « plainte n° 12 ». */
  number: number;
  source: ComplaintSource;
  category: ComplaintCategory;
  status: ComplaintStatus;
  createdAt: string;
  updatedAt: string;
  closedAt?: string;
  closedBy?: { id?: string; email?: string };
  hasJournal: boolean;
  journalBytes?: number;
  accountId?: string;
  phone?: string;
  household?: { tenantId: string; name: string };
  /** List entries: the first message's start and the thread's size. */
  preview?: string;
  messageCount?: number;
  lastFrom?: 'customer' | 'admin';
  lastMessageAt?: string;
  /** The detail only. */
  journal?: string;
  messages?: ComplaintMessage[];
};

export type ComplaintCounts = { open: number; answered: number; closed: number; openOverDay: number };

/** GET /api/v1/admin/complaints */
export type Complaints = { complaints: Complaint[]; counts: ComplaintCounts };

/** GET /api/v1/admin/complaints/{ref} */
export type ComplaintDetail = { complaint: Complaint; limits: { message: number } };

/** POST /api/v1/admin/complaints/{ref}/answer */
export type ComplaintAnswer = {
  complaint: Complaint;
  message: ComplaintMessage;
  delivery: { pushed: number; pushFailed: number; whatsapp: '' | 'queued' | 'not_opted_in' | 'off' };
};

/** GET|PUT /api/v1/admin/settings/complaint-admins */
export type ComplaintAdmins = { numbers: string[]; max: number };
