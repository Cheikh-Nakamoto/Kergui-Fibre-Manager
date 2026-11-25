import { keepPreviousData, useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

import { checkNewDevices, enforceAutoReject } from "../alerts/alerts";
import { useArrivals } from "../alerts/arrivals";
import { approvals } from "./approvals";
import { hubBackup } from "./hubBackup";
import { labelSync, onLabelsChange } from "./labels";
import { ownerDevices } from "./ownerDevices";
import { ApiError, api, core } from "../core/client";
import type { Device, Usage, WiFiSSID, WiFiSSIDChange } from "../core/types";
import { useAccount } from "../licensing/account";
import { config } from "../licensing/config";
import { useEntitlement } from "../licensing/entitlement";
import { createSampler, CURVE_MONTHS } from "../lib/usage";
import { useSession } from "../session/store";
import { probeAtHome } from "./atHome";
import { useDemoRemote } from "./demoRemote";
import { syncRelayPolicy } from "./relayPolicy";
import {
  detectRemote,
  remoteCutInternet,
  remoteRequest,
  remoteDevices,
  remoteRestoreInternet,
  remoteSetBlocked,
  remoteSetWiFiCut,
  remoteSSIDs,
  remoteUpdateSSID,
  remoteUsage,
  remoteWiFiDenied,
  type HouseResult,
  type RemoteDevices,
  type RemoteHousehold,
} from "./remote";

/** The gate's box for a command the core signs for the relay. */
const remoteOpts = () => ({ boxId: useEntitlement.getState().boxId ?? "" });

// Router writes are never optimistic: the core rereads the box after each
// write, so the lists are refetched from it.

export const keys = {
  devices: ["devices"] as const,
  ssids: ["wifi", "ssids"] as const,
  wifiDenied: ["wifi", "denied"] as const,
  filter: ["filter"] as const,
  journal: ["journal"] as const,
  autoReject: ["autoReject"] as const,
  usage: ["usage", "current"] as const,
  usageMonth: (period: string) => ["usage", "month", period] as const,
  deviceUsage: (mac: string) => ["usage", "device", mac.toLowerCase()] as const,
  remote: ["remote", "mode"] as const,
  atHome: ["remote", "atHome"] as const,
};

// Remote mode (#46): set once the household's relay is known, so what still
// reads the box on its own (the usage sampler, auto-reject) leaves it to the
// relay rather than opening a second admin session.
let remoteActive = false;

// Names taken from the relay (#95) belong in this phone's backup too.
onLabelsChange(() => hubBackup.changed());

/** What useRemoteMode answers. */
export type RemoteMode = {
  /** The household to go through the relay for now; null: talk to the box directly. */
  data: RemoteHousehold | null;
  decided: boolean;
  error: unknown;
  /** The household's relay, at home too (the whole-house cut goes through it). */
  household: RemoteHousehold | null;
  /** The owner's phone is at home, on the box's network: it talks to the box itself (#94). */
  atHome: boolean;
};

/**
 * The household this phone reaches through its relay, or null for local
 * mode (no hub, not signed in, no relay, the demo). Decided by the core from
 * the bundle the owner's key signed, and, on the owner's phone, by where the
 * phone is (#94, « proprio prioritaire »): at home it talks to the box
 * directly and the relay yields; away it goes through the relay. A member's
 * phone has no box password: it always goes through the relay.
 */
export function useRemoteMode(): RemoteMode {
  const demo = useSession((s) => s.session?.mode === "demo");
  const routerUrl = useSession((s) => (s.session?.mode === "router" ? s.session.routerUrl : ""));
  const accountId = useAccount((s) => s.account?.accountId ?? "");
  // Without a hub, an account or with the demo there is nothing to ask:
  // local mode at once, and the local screens lose no time to it.
  const possible = !demo && !!config.hubUrl && !!accountId;
  const q = useQuery({
    queryKey: [...keys.remote, accountId],
    queryFn: detectRemote,
    enabled: possible,
    staleTime: 60_000,
    refetchInterval: 5 * 60_000,
  });
  const household = possible ? (q.data ?? null) : null;
  const canGoDirect = !!household && !household.member && !!routerUrl;
  // Asked again every 20 s and on every return to the app: walking out of
  // the Wi-Fi or back in switches the mode within a refresh or two. A probe
  // that has not answered yet keeps the last answer, so the screens do not
  // flicker between the two.
  const home = useQuery({
    queryKey: [...keys.atHome, routerUrl],
    queryFn: () => probeAtHome(),
    enabled: canGoDirect,
    staleTime: 15_000,
    refetchInterval: 20_000,
    retry: false,
  });
  const atHome = canGoDirect && home.data === true;
  const data = atHome ? null : household;
  useEffect(() => {
    remoteActive = !!data;
  }, [data]);
  if (!possible) return { data: null, decided: true, error: null, household: null, atHome: false };
  const decided = (q.isSuccess || q.isError) && (!canGoDirect || home.isFetched);
  return { data, decided, error: q.error, household, atHome };
}

/**
 * A local read that cannot reach the box may mean the phone just left home:
 * ask again where it is, so the next refresh goes through the relay instead
 * of showing the error again.
 */
async function local<T>(qc: QueryClient, read: () => Promise<T>): Promise<T> {
  try {
    return await read();
  } catch (e) {
    if (e instanceof ApiError && (e.status === 502 || e.status === 504 || e.status === 0)) {
      void qc.invalidateQueries({ queryKey: keys.atHome });
    }
    throw e;
  }
}

// When the owner's phone leaves home, the relay may still be paused for it
// (#94): a command it signs takes the box back at once, rather than leaving
// the relay's list as old as the moment the owner came home. A read, at most
// once a minute.
let lastNudge = 0;
function nudgePausedRelay(h: RemoteHousehold, relayState: string) {
  if (relayState !== "paused" || h.member || Date.now() - lastNudge < 60_000) return;
  lastNudge = Date.now();
  void remoteRequest(h, "GET", "/api/devices", undefined, remoteOpts()).catch(() => undefined);
}

/** The Devices tab's data: the list, and in remote mode its as_of and the relay's state. */
export type DevicesView = { devices: Device[]; remote: Omit<RemoteDevices, "devices"> | null };

// Consumption is measured by the phone itself: each foreground refresh (the
// device list, the Conso tab) samples the box's counters, at most every 10 s
// since the box keeps one admin session. The watch service samples too.
// false once the box answered that it has no counters (501).
let usageMeasurable = true;
export const sampleUsage = createSampler(async () => {
  if (remoteActive) return;
  try {
    await api.sampleUsage();
    usageMeasurable = true;
  } catch (e) {
    if (e instanceof ApiError && e.notSupported) usageMeasurable = false;
    throw e;
  }
}, 10_000);

/**
 * Marks the approved devices (#58) with the household's list: the core marks
 * its own, a list from the relay has none.
 */
function markApproved(devices: Device[], approved: Set<string> | null): Device[] {
  if (!approved) return devices;
  return devices.map((d) => ({ ...d, Approved: approved.has(d.MAC.toLowerCase()) || !!d.Approved }));
}

async function localDevices(): Promise<DevicesView> {
  const demo = useSession.getState().session?.mode === "demo";
  // The household's approvals first (the hub's, when signed in), so the
  // list and the alerts agree with the other phones. Best effort.
  const approved = await approvals.sync().catch(() => null);
  // The owner's phones (#87), so the core marks them Owner. Best effort.
  await ownerDevices.sync().catch(() => undefined);
  const devices = markApproved(await api.devices(), approved);
  // On the owner's phone, the MAC the core found it under goes to the hub.
  void ownerDevices.declare(devices);
  // Best effort: an alert that cannot be raised must not hide the list,
  // nor a sample that could not be taken.
  await checkNewDevices(devices, { demo }).catch(() => 0);
  // Where the Android watch service does not run the rule (the demo,
  // iOS, alerts off), the open app does.
  await enforceAutoReject({ demo }).catch(() => 0);
  // The relay's copy of the rule (#36): an accepted device, a rule turned on
  // elsewhere in the app, a launch after a failed upload. Cheap when nothing
  // changed; never in the way of the list.
  void syncRelayPolicy({ demo });
  // The household's backup on the hub (#89): the hub's after a sign-in or
  // on an empty phone, this phone's once a day. Never in the way of the list.
  void hubBackup.tick();
  await sampleUsage();
  return { devices, remote: null };
}

/**
 * The device list. In remote mode it is the list the relay signed, through
 * the hub, with its as_of; the box itself is never asked, even on its Wi-Fi.
 */
export function useDevicesView() {
  const qc = useQueryClient();
  const mode = useRemoteMode();
  const h = mode.data ?? null;
  const q = useQuery({
    queryKey: [...keys.devices, h ? `remote:${h.tenantId}` : "local"],
    queryFn: async (): Promise<DevicesView> => {
      if (!h) {
        const view = await local(qc, localDevices);
        // At home the owner's names still go to the relay (#95), for when
        // this phone is away and for the members. Never in the way of the list.
        void labelSync.tick(mode.household);
        return view;
      }
      // The owner's phones on the relay's list (#87): the core marks them.
      await ownerDevices.sync({ tenantId: h.tenantId }).catch(() => undefined);
      const r = await remoteDevices(h);
      nudgePausedRelay(h, r.relay.state);
      // The household's names (#95): the relay's list may not have this
      // phone's yet, nor this phone the ones given through the relay.
      const named = await labelSync
        .view(h, { agentId: r.relay.agentId ?? r.agentId ?? null, asOf: r.asOf, devices: r.devices })
        .catch(() => r.devices);
      const devices = markApproved(named, await approvals.sync({ remote: true }).catch(() => null));
      await checkNewDevices(devices).catch(() => 0);
      return { devices, remote: { asOf: r.asOf, relay: r.relay } };
    },
    enabled: mode.decided,
    refetchInterval: 30_000,
    // Switching between home and away keeps the list on screen until the
    // other side answers: no blank screen, nothing lost.
    placeholderData: keepPreviousData,
  });
  return { ...q, isFetching: q.isFetching || !mode.decided, error: q.error ?? mode.error, remote: h };
}

export function useDevices() {
  const v = useDevicesView();
  return { ...v, data: v.data?.devices };
}

/**
 * The current month's consumption per device (free). Samples first, so a
 * pull-to-refresh shows what was used up to now. measurable is false when the
 * box model has no byte counters.
 */
export function useUsage() {
  const qc = useQueryClient();
  const mode = useRemoteMode();
  const h = mode.data ?? null;
  return useQuery({
    queryKey: [...keys.usage, h ? `remote:${h.tenantId}` : "local"],
    // In remote mode (#50) the relay samples and reads the box: asOf is when.
    queryFn: async (): Promise<{ usage: Usage[]; measurable: boolean; asOf?: string }> => {
      if (h) {
        const r = await remoteUsage(h, remoteOpts());
        return { usage: r.data, measurable: true, asOf: r.asOf };
      }
      return local(qc, async () => {
        await sampleUsage();
        return { usage: await api.usage(), measurable: usageMeasurable };
      });
    },
    enabled: mode.decided,
    refetchInterval: h ? 60_000 : 30_000,
    placeholderData: keepPreviousData,
  });
}

/**
 * A past month's consumption per device (Max: the monthly filter). Nothing is
 * sampled for it, and it does not refresh: that month is over. Only asked
 * when enabled, so a locked screen never reaches the core.
 */
export function useUsageMonth(period: string, enabled: boolean) {
  // Past months are not on the relay's allow-list (#50): local mode only.
  const remote = !!useRemoteMode().data;
  return useQuery({
    queryKey: keys.usageMonth(period),
    queryFn: async (): Promise<{ usage: Usage[]; measurable: boolean }> => ({
      usage: await api.usage(period),
      measurable: usageMeasurable,
    }),
    enabled: enabled && !remote,
  });
}

/** One device's last CURVE_MONTHS months, oldest first (Max: the history). */
export function useDeviceUsage(mac: string, enabled: boolean) {
  // The history is not on the relay's allow-list (#50): local mode only.
  const remote = !!useRemoteMode().data;
  return useQuery({
    queryKey: keys.deviceUsage(mac),
    queryFn: () => api.deviceUsage(mac, CURVE_MONTHS),
    enabled: enabled && !remote,
    refetchInterval: 60_000,
  });
}

/**
 * Blocks (Basic) or unblocks (free) a device. In remote mode the core signs
 * it with the owner's key, the relay runs it, and the mutation resolves only
 * once the core has checked the relay's signature over the answer: that is
 * the ✅. A relay that is not there fails it with a sentence, never with a
 * login to the box from this phone.
 */
export function useSetBlocked() {
  const qc = useQueryClient();
  const mode = useRemoteMode();
  return useMutation({
    mutationFn: async ({ mac, blocked }: { mac: string; blocked: boolean }): Promise<{ remote: boolean }> => {
      const h = mode.data;
      if (h) {
        await remoteSetBlocked(h, mac, blocked, { boxId: useEntitlement.getState().boxId ?? "" });
        return { remote: true };
      }
      await (blocked ? api.block(mac) : api.unblock(mac));
      return { remote: false };
    },
    onSettled: () => qc.invalidateQueries({ queryKey: keys.devices }),
  });
}

/** MACs compare in the core's canonical form: lower case. */
export const sameMac = (a: string, b: string) => a.toLowerCase() === b.toLowerCase();

/**
 * The box's Wi-Fi deny list ("Accès coupé"), lower case. null when the box
 * model has no Wi-Fi access control (the core answers 501): the screens then
 * hide "Couper l'accès" rather than show an error.
 */
export function useWiFiDenied() {
  const mode = useRemoteMode();
  const h = mode.data ?? null;
  return useQuery({
    // Through the relay (#50): null when this box model keeps Wi-Fi access
    // disabled from afar (not validated on hardware).
    queryKey: [...keys.wifiDenied, h ? "remote" : "local"],
    enabled: mode.decided,
    queryFn: async (): Promise<string[] | null> => {
      if (h) return remoteWiFiDenied(h, remoteOpts());
      try {
        return (await api.wifiBlocked()).map((m) => m.toLowerCase());
      } catch (e) {
        if (e instanceof ApiError && e.notSupported) return null;
        throw e;
      }
    },
    refetchInterval: 30_000,
  });
}

/** Cuts (Basic) or gives back (free) a device's access to the Wi-Fi. */
export function useSetWiFiCut() {
  const qc = useQueryClient();
  const mode = useRemoteMode();
  return useMutation({
    mutationFn: async ({ mac, cut }: { mac: string; cut: boolean }): Promise<unknown> => {
      const h = mode.data;
      if (h) return remoteSetWiFiCut(h, mac, cut, remoteOpts());
      return cut ? api.wifiBlock(mac) : api.wifiUnblock(mac);
    },
    onSettled: () =>
      Promise.all([
        qc.invalidateQueries({ queryKey: keys.wifiDenied }),
        qc.invalidateQueries({ queryKey: keys.devices }),
        // Given back: accepted under auto-reject.
        qc.invalidateQueries({ queryKey: keys.autoReject }),
      ]),
  });
}

/**
 * « Renommer » (#95): the same name at home and through the relay. The
 * owner's phone renames in its core and sends its names to the relay (at
 * home too); a member's phone renames through the relay.
 */
export function useRename() {
  const qc = useQueryClient();
  const mode = useRemoteMode();
  return useMutation({
    mutationFn: async ({ mac, name }: { mac: string; name: string }) => {
      const demo = useSession.getState().session?.mode === "demo";
      await labelSync.rename(demo ? null : mode.household, mac, name);
      // Giving it a name approves it (#58): it is not signalled again, on
      // any phone of the household.
      if (name.trim()) {
        await approvals.approve(mac, { remote: !!mode.household?.member }).catch((e) => {
          // A member the hub does not let approve: the name stands.
          if (!mode.household?.member) throw e;
        });
        useArrivals.getState().dismiss(mac);
      }
      hubBackup.changed();
    },
    // A renamed device is accepted under auto-reject.
    onSettled: () =>
      Promise.all([
        qc.invalidateQueries({ queryKey: keys.devices }),
        qc.invalidateQueries({ queryKey: keys.autoReject }),
      ]),
  });
}

/**
 * Auto-reject's setting (Max), kept in the core: the watch service reads
 * the same one. null when the box model has no Wi-Fi access control (501):
 * the setting is then hidden.
 */
export function useAutoReject() {
  return useQuery({
    queryKey: keys.autoReject,
    queryFn: async () => {
      try {
        return await api.autoReject();
      } catch (e) {
        if (e instanceof ApiError && e.notSupported) return null;
        throw e;
      }
    },
    refetchInterval: 30_000,
  });
}

/** Turns auto-reject on (Max, accepting every device present) or off (free). */
export function useSetAutoReject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (enabled: boolean) => api.setAutoReject(enabled),
    onSuccess: (status) => {
      qc.setQueryData(keys.autoReject, status);
      // The household's relay gets the new rule at once (#36). Best effort:
      // the setting on this phone stands whatever the hub answers.
      void syncRelayPolicy({ demo: useSession.getState().session?.mode === "demo" });
    },
    onSettled: () => qc.invalidateQueries({ queryKey: keys.autoReject }),
  });
}

/**
 * « Approuver » (#58, free): the device is never signalled again, on any
 * phone of the household. Under auto-reject it is accepted too.
 */
export function useApprove() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (mac: string) => {
      await approvals.approve(mac, { remote: remoteActive });
      useArrivals.getState().dismiss(mac);
      // Under auto-reject, accepted too; the relay enforces it remotely.
      if (!remoteActive) await api.acceptDevice(mac).catch(() => undefined);
      hubBackup.changed();
    },
    onSettled: () =>
      Promise.all([
        qc.invalidateQueries({ queryKey: keys.devices }),
        qc.invalidateQueries({ queryKey: keys.autoReject }),
      ]),
  });
}

/** « Retirer l'approbation »: signalled again on its next arrival. */
export function useWithdrawApproval() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (mac: string) => {
      await approvals.withdraw(mac, { remote: remoteActive });
      hubBackup.changed();
    },
    onSettled: () => qc.invalidateQueries({ queryKey: keys.devices }),
  });
}

/** The demo's visitor joins or leaves its box (#58), to show an arrival. */
export function useDemoVisitor() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (present: boolean) => api.demoVisitor(present),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.devices }),
  });
}

/** Accepts a device under auto-reject (free). */
export function useAcceptDevice() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (mac: string) => api.acceptDevice(mac),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.autoReject }),
  });
}

/** The Wi-Fi networks; in remote mode (#50) as the relay read them, with when (asOf). */
export function useSSIDsView() {
  const qc = useQueryClient();
  const mode = useRemoteMode();
  const h = mode.data ?? null;
  return useQuery({
    queryKey: [...keys.ssids, h ? `remote:${h.tenantId}` : "local"],
    queryFn: async (): Promise<{ ssids: WiFiSSID[]; asOf: string | null }> => {
      if (h) {
        const r = await remoteSSIDs(h, remoteOpts());
        return { ssids: r.data, asOf: r.asOf };
      }
      return { ssids: await local(qc, () => api.ssids()), asOf: null };
    },
    enabled: mode.decided,
    placeholderData: keepPreviousData,
  });
}

export function useSSIDs() {
  const v = useSSIDsView();
  return { ...v, data: v.data?.ssids };
}

export function useUpdateSSID() {
  const qc = useQueryClient();
  const mode = useRemoteMode();
  return useMutation({
    mutationFn: async ({ id, change }: { id: string; change: WiFiSSIDChange }): Promise<unknown> => {
      const h = mode.data;
      if (h) return remoteUpdateSSID(h, id, change, remoteOpts());
      return api.updateSSID(id, change);
    },
    onSettled: () => qc.invalidateQueries({ queryKey: keys.ssids }),
  });
}

export function useFilter() {
  const mode = useRemoteMode();
  // The MAC filter switch stays on the box's own Wi-Fi: in remote mode the
  // box is never asked (RFC-005 rule 1).
  return useQuery({ queryKey: keys.filter, queryFn: api.filter, enabled: mode.decided && !mode.data });
}

/**
 * Cut / restore Internet for the whole house (#50): through the relay, which
 * spares itself, the household's other relays and the protected devices.
 * Cutting is Basic, restoring free. In the demo (no hub), once the demo
 * relay is paired, the demo box's devices are blocked one by one, simulated.
 */
export function useHouseInternet() {
  const qc = useQueryClient();
  const mode = useRemoteMode();
  const demo = useSession((s) => s.session?.mode === "demo");
  const demoRelay = useDemoRemote((s) => s.relay);
  // At home too (#94): only the relay cuts the whole house, and the owner's
  // command takes the box back from the owner's own session for it.
  const available = !!mode.household || (demo && !!demoRelay);
  const run = useMutation({
    mutationFn: async (cut: boolean): Promise<HouseResult & { action: "cut" | "restore" }> => {
      const action = cut ? "cut" : "restore";
      const h = mode.household;
      if (h) return { ...(await (cut ? remoteCutInternet(h, remoteOpts()) : remoteRestoreInternet(h, remoteOpts()))), action };
      return { ...(await demoHouse(cut)), action };
    },
    onSettled: () => qc.invalidateQueries({ queryKey: keys.devices }),
  });
  return { available, run };
}

// What the demo's whole-house cut blocked, in memory.
let demoHouseCut: string[] = [];

async function demoHouse(cut: boolean): Promise<HouseResult> {
  if (!cut) {
    const macs = demoHouseCut;
    demoHouseCut = [];
    for (const m of macs) await api.unblock(m);
    return { cut: [], restored: macs, spared: [], failed: [] };
  }
  const devices = await api.devices();
  const out: HouseResult = { cut: [], restored: [], spared: [], failed: [] };
  for (const d of devices) {
    if (d.Blocked) continue;
    await api.block(d.MAC);
    out.cut.push(d.MAC);
  }
  demoHouseCut = [...demoHouseCut, ...out.cut];
  return out;
}

export function useSetFilter() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (enabled: boolean) => api.setFilter(enabled),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.filter }),
  });
}

/**
 * The core's journal. It goes through the native module rather than the API,
 * so the screen still has something to show when the core is not pointed at
 * a box — which is exactly when someone opens it.
 */
export function useJournal() {
  return useQuery({
    queryKey: keys.journal,
    queryFn: () => core.journal(),
    refetchInterval: 5_000,
    // Nothing here is worth keeping between visits: the ring buffer is the
    // truth, and it moves.
    gcTime: 0,
  });
}
