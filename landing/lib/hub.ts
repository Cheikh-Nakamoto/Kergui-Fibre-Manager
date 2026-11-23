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
};

export type Account = {
  accountId: string;
  phone: string;
  tenantId?: string;
  createdAt: string;
  subscriptions: { boxId: string; tier: string; expiresAt: string; active: boolean }[];
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

export type WhatsAppStatus = { linked: boolean; number?: string; connected: boolean };
