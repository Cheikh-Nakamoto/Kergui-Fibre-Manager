import { useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import * as SecureStore from 'expo-secure-store';
import { create } from 'zustand';

import { useArrivals } from '../alerts/arrivals';
import { ApiError, api } from '../core/client';
import type { Device, WiFiLockCloseRequest, WiFiLockStatus } from '../core/types';
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';
import { lockView, type Trust } from '../lib/trust';
import { approvals } from './approvals';
import { hubBackup } from './hubBackup';
import { keys, sameMac, useRemoteMode, useWiFiDenied, type DevicesView } from './queries';
import { remoteSetBlocked, remoteSetWiFiCut, type RemoteHousehold } from './remote';

// The closed Wi-Fi in the app (#108, RFC-007 D12), « Réservé à la maison »
// (#111). The box is the truth for display: every write rereads it.
//
// Through the household's relay the core's routes are #109's: until then the
// whole of it is local, and hidden in remote mode.

export const lockKeys = {
  status: ['wifi', 'lock'] as const,
  preview: ['wifi', 'lock', 'preview'] as const,
  relays: ['wifi', 'relays'] as const,
};

/** A core whose box cannot reserve its Wi-Fi (501), or one from before the routes (404). */
const unsupported = (e: unknown) => e instanceof ApiError && (e.status === 501 || e.status === 404);

/**
 * The box's closed-Wi-Fi state; null when it cannot do it (D11: then the
 * card, the « Autorisés » tab and the « Autorisé » state are hidden), or in
 * remote mode (#109).
 */
export function useWiFiLock() {
  const mode = useRemoteMode();
  const local = mode.decided && !mode.data;
  const q = useQuery({
    queryKey: lockKeys.status,
    enabled: local,
    queryFn: async (): Promise<WiFiLockStatus | null> => {
      try {
        return await api.wifiLock();
      } catch (e) {
        if (unsupported(e)) return null;
        throw e;
      }
    },
    // An add window counts down: read the box more often meanwhile.
    refetchInterval: (query) => (query.state.data?.state === 'window' || query.state.data?.state === 'busy' ? 10_000 : 30_000),
  });
  return { ...q, data: local ? (q.data ?? null) : null, remote: !!mode.data };
}

/** The household's relays the core knows on this box (always admitted). */
export function useRelayDevices(enabled = true) {
  return useQuery({
    queryKey: lockKeys.relays,
    enabled,
    queryFn: () => api.relayDevices().catch(() => [] as string[]),
    staleTime: 60_000,
  });
}

/** What a reservation would write (free, writes nothing). */
export function useReservePreview(req: WiFiLockCloseRequest, enabled: boolean) {
  return useQuery({
    queryKey: [...lockKeys.preview, req],
    enabled,
    queryFn: () => api.wifiLockPreview(req),
    retry: false,
  });
}

/** Everything a closed-Wi-Fi write changes, read again from the box. */
function rereadAll(qc: QueryClient) {
  return Promise.all([
    qc.invalidateQueries({ queryKey: lockKeys.status }),
    qc.invalidateQueries({ queryKey: lockKeys.preview }),
    qc.invalidateQueries({ queryKey: keys.devices }),
    qc.invalidateQueries({ queryKey: keys.wifiDenied }),
    qc.invalidateQueries({ queryKey: keys.blocked }),
    qc.invalidateQueries({ queryKey: keys.autoReject }),
  ]);
}

export type LockAction =
  | { kind: 'reserve'; req: WiFiLockCloseRequest }
  | { kind: 'reopen' }
  | { kind: 'window'; action: 'open' | 'extend' | 'close' };

/** « Réserver », « Rouvrir », and the add window's three buttons. */
export function useLockAction() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (a: LockAction): Promise<unknown> => {
      if (a.kind === 'reserve') return api.wifiLockClose(a.req);
      if (a.kind === 'reopen') return api.wifiLockOpen();
      return api.wifiLockWindow(a.action);
    },
    onSettled: () => rereadAll(qc),
  });
}

// --- A device's state: Autorisé, À vérifier, Bloqué (D1, D5) ---------------

/** Where a device stands before a change: what « Annuler » puts back (R6). */
export type TrustFrom = {
  trust: Trust;
  /** « Ne peut plus se connecter »: refused at the Wi-Fi too. */
  cut: boolean;
  /** Refused at the Wi-Fi by an old « Couper l'accès » the core does not track. */
  legacyCut: boolean;
};

export type TrustChange = { mac: string; name: string; to: Trust; cutWiFi: boolean; from: TrustFrom; reserved: boolean };

type TrustState = {
  /** The devices whose change waits for the box to be read again (« En cours… »). */
  pending: Record<string, true>;
  /** The last change « Annuler » can undo; it stays until the next one (R6). */
  last: (TrustChange & { n: number }) | null;
  dismiss: () => void;
};

export const useTrustState = create<TrustState>((set) => ({
  pending: {},
  last: null,
  dismiss: () => set({ last: null }),
}));

const opts = () => ({ boxId: useEntitlement.getState().boxId ?? '' });

/** The writes for one change, at home (the core) or through the relay. */
async function applyTrust(c: TrustChange, h: RemoteHousehold | null): Promise<void> {
  const { mac, to, cutWiFi, from } = c;
  if (h) {
    if (to === 'blocked') {
      if (from.trust !== 'blocked') await remoteSetBlocked(h, mac, true, opts());
      if (cutWiFi !== from.cut) await remoteSetWiFiCut(h, mac, cutWiFi, opts());
      return;
    }
    if (from.trust === 'blocked') {
      await remoteSetBlocked(h, mac, false, opts());
      if (from.cut) await remoteSetWiFiCut(h, mac, false, opts());
    }
    if (to === 'authorised') await approvals.approve(mac, { remote: true });
    else if (from.trust === 'authorised') await approvals.withdraw(mac, { remote: true });
    return;
  }
  if (to === 'blocked') {
    // The core withdraws the authorisation with the block (D1); the hub's
    // copy is withdrawn here.
    if (from.trust !== 'blocked' || cutWiFi !== from.cut) {
      await (from.cut || cutWiFi ? api.block(mac, { cutWiFi }) : api.block(mac));
    }
    if (!cutWiFi && from.legacyCut) await api.wifiUnblock(mac);
    if (from.trust === 'authorised') await approvals.forgetOnHub(mac).catch(() => undefined);
    return;
  }
  if (from.trust === 'blocked') {
    // Débloquer: the firewall rule and the tracked refusal (D5), and an old
    // refusal the core does not track.
    await api.unblock(mac);
    if (from.legacyCut) await api.wifiUnblock(mac);
  }
  if (to === 'authorised') {
    await approvals.approve(mac);
    useArrivals.getState().dismiss(mac);
    // Under auto-reject, accepted too.
    await api.acceptDevice(mac).catch(() => undefined);
  } else if (from.trust === 'authorised') {
    await approvals.withdraw(mac);
  }
}

/** The device list with one device's authorisation changed, before the box is read again. */
function markApproval(qc: QueryClient, mac: string, approved: boolean) {
  qc.setQueriesData<DevicesView>({ queryKey: keys.devices }, (v) =>
    v && Array.isArray(v.devices) && 'remote' in v
      ? { ...v, devices: v.devices.map((d: Device) => (sameMac(d.MAC, mac) ? { ...d, Approved: approved } : d)) }
      : v,
  );
}

/**
 * Sets a device's state (D5). Open, « Autoriser » and « À vérifier » write
 * nothing on the box and show at once; anything else shows « En cours… »
 * until the box is read again (D12). A block or « À vérifier » is offered
 * an « Annuler » that stays until the next action (R6); an undo sets the
 * state back, it is not a second blind write.
 */
export function useSetTrust() {
  const qc = useQueryClient();
  const mode = useRemoteMode();
  return useMutation({
    mutationFn: async (c: TrustChange & { undo?: boolean }): Promise<{ remote: boolean }> => {
      await applyTrust(c, mode.data ?? null);
      hubBackup.changed();
      return { remote: !!mode.data };
    },
    onMutate: (c) => {
      const instant = !c.reserved && c.from.trust !== 'blocked' && c.to !== 'blocked' && !mode.data;
      if (instant) markApproval(qc, c.mac, c.to === 'authorised');
      else useTrustState.setState((s) => ({ pending: { ...s.pending, [c.mac.toLowerCase()]: true } }));
      // A new action ends the last one's « Annuler » (R6).
      useTrustState.setState((s) => ({ last: !c.undo && c.to !== 'authorised' ? { ...c, n: (s.last?.n ?? 0) + 1 } : null }));
    },
    onError: (_e, c) => {
      useTrustState.setState((s) => (s.last?.mac === c.mac ? { last: null } : s));
    },
    onSettled: async (_r, _e, c) => {
      await rereadAll(qc);
      useTrustState.setState((s) => {
        const pending = { ...s.pending };
        delete pending[c.mac.toLowerCase()];
        return { pending };
      });
    },
  });
}

/** Where a device stands (D1), and what its choices depend on. */
export function useTrustContext() {
  const lock = useWiFiLock();
  const denied = useWiFiDenied();
  const view = lockView(lock.data);
  const tracked = lock.data?.cut ?? [];
  const onBox = denied.data ?? [];
  return {
    /** The box can reserve its Wi-Fi (D11). */
    capable: !!lock.data,
    reserved: !!view?.reserved,
    /** The box can refuse a device at the Wi-Fi: the switch is offered (D11). */
    wifiControl: Array.isArray(denied.data),
    from: (d: Pick<Device, 'MAC' | 'Blocked' | 'Approved'>): TrustFrom => {
      const legacyCut = onBox.some((m) => sameMac(m, d.MAC)) && !tracked.some((m) => sameMac(m, d.MAC));
      const cut = legacyCut || tracked.some((m) => sameMac(m, d.MAC)) || onBox.some((m) => sameMac(m, d.MAC));
      const trust: Trust = d.Blocked || cut ? 'blocked' : d.Approved ? 'authorised' : 'check';
      return { trust, cut, legacyCut };
    },
  };
}

// --- What the app remembers of the household's answers ---------------------

/** A visitors' question's answer (D2, R9): « Je ne sais pas » keeps it open and asks again. */
export type VisitorAnswer = 'yes' | 'no' | 'unsure';

type BoxPrefs = {
  /** « Voici les appareils de la maison » was done once (R5). */
  reviewed?: boolean;
  /** By network name: the answer to « Ce réseau est-il pour vos visiteurs ? ». */
  visitors?: Record<string, VisitorAnswer>;
};

export const LOCK_PREFS_KEY = 'kergui.wifiLock.prefs';

type PrefsState = {
  loaded: boolean;
  byBox: Record<string, BoxPrefs>;
  load: () => Promise<void>;
  update: (box: string, patch: BoxPrefs) => Promise<void>;
};

/** Kept in SecureStore, the only store the app has, keyed by box. */
export const useLockPrefs = create<PrefsState>((set, get) => ({
  loaded: false,
  byBox: {},
  load: async () => {
    if (get().loaded) return;
    try {
      const raw = await SecureStore.getItemAsync(LOCK_PREFS_KEY);
      const byBox = raw ? (JSON.parse(raw) as Record<string, BoxPrefs>) : {};
      set({ loaded: true, byBox: byBox && typeof byBox === 'object' ? byBox : {} });
    } catch {
      set({ loaded: true });
    }
  },
  update: async (box, patch) => {
    const cur = get().byBox[box] ?? {};
    const next = { ...get().byBox, [box]: { ...cur, ...patch, visitors: { ...cur.visitors, ...patch.visitors } } };
    set({ byBox: next });
    await SecureStore.setItemAsync(LOCK_PREFS_KEY, JSON.stringify(next)).catch(() => undefined);
  },
}));

/** The box these answers are for: the demo's own, or the box's address. */
export function useBoxKey(): string {
  return useSession((s) => (s.session?.mode === 'router' ? s.session.routerUrl : s.session?.mode === 'demo' ? 'demo' : ''));
}

// The bottom panel of the card (« Gérer »): in its own light module, so
// ActionError can close it without loading the rest of this one (#134).
export { useLockPanel } from './lockPanel';
