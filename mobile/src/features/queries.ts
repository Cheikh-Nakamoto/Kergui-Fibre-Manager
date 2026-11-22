import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

import { checkNewDevices, enforceAutoReject } from "../alerts/alerts";
import { ApiError, api, core } from "../core/client";
import type { Device, Usage, WiFiSSIDChange } from "../core/types";
import { useAccount } from "../licensing/account";
import { config } from "../licensing/config";
import { useEntitlement } from "../licensing/entitlement";
import { createSampler, CURVE_MONTHS } from "../lib/usage";
import { useSession } from "../session/store";
import {
  detectRemote,
  remoteDevices,
  remoteSetBlocked,
  type RemoteDevices,
  type RemoteHousehold,
} from "./remote";

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
};

// Remote mode (#46): set once the household's relay is known, so what still
// reads the box on its own (the usage sampler, auto-reject) leaves it to the
// relay rather than opening a second admin session.
let remoteActive = false;

/**
 * The household this phone reaches through its relay, or null for local
 * mode (no hub, not signed in, no relay, the demo). Decided by the core from
 * the bundle the owner's key signed.
 */
export function useRemoteMode(): { data: RemoteHousehold | null; decided: boolean; error: unknown } {
  const demo = useSession((s) => s.session?.mode === "demo");
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
  const data = possible ? (q.data ?? null) : null;
  useEffect(() => {
    remoteActive = !!data;
  }, [data]);
  if (!possible) return { data: null, decided: true, error: null };
  return { data, decided: q.isSuccess || q.isError, error: q.error };
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

async function localDevices(): Promise<DevicesView> {
  const devices = await api.devices();
  // Best effort: an alert that cannot be raised must not hide the list,
  // nor a sample that could not be taken.
  await checkNewDevices(devices).catch(() => 0);
  // Where the Android watch service does not run the rule (the demo,
  // iOS, alerts off), the open app does.
  await enforceAutoReject({ demo: useSession.getState().session?.mode === "demo" }).catch(() => 0);
  await sampleUsage();
  return { devices, remote: null };
}

/**
 * The device list. In remote mode it is the list the relay signed, through
 * the hub, with its as_of; the box itself is never asked, even on its Wi-Fi.
 */
export function useDevicesView() {
  const mode = useRemoteMode();
  const h = mode.data ?? null;
  const q = useQuery({
    queryKey: [...keys.devices, h ? `remote:${h.tenantId}` : "local"],
    queryFn: async (): Promise<DevicesView> => {
      if (!h) return localDevices();
      const r = await remoteDevices(h);
      await checkNewDevices(r.devices).catch(() => 0);
      return { devices: r.devices, remote: { asOf: r.asOf, relay: r.relay } };
    },
    enabled: mode.decided,
    refetchInterval: 30_000,
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
  return useQuery({
    queryKey: keys.usage,
    queryFn: async (): Promise<{ usage: Usage[]; measurable: boolean }> => {
      await sampleUsage();
      return { usage: await api.usage(), measurable: usageMeasurable };
    },
    refetchInterval: 30_000,
  });
}

/**
 * A past month's consumption per device (Max: the monthly filter). Nothing is
 * sampled for it, and it does not refresh: that month is over. Only asked
 * when enabled, so a locked screen never reaches the core.
 */
export function useUsageMonth(period: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.usageMonth(period),
    queryFn: async (): Promise<{ usage: Usage[]; measurable: boolean }> => ({
      usage: await api.usage(period),
      measurable: usageMeasurable,
    }),
    enabled,
  });
}

/** One device's last CURVE_MONTHS months, oldest first (Max: the history). */
export function useDeviceUsage(mac: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.deviceUsage(mac),
    queryFn: () => api.deviceUsage(mac, CURVE_MONTHS),
    enabled,
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
  const remote = !!mode.data;
  return useQuery({
    // Through the relay, cutting Wi-Fi access is #50: hidden until then,
    // and the box is not asked.
    queryKey: [...keys.wifiDenied, remote ? "remote" : "local"],
    enabled: mode.decided,
    queryFn: async (): Promise<string[] | null> => {
      if (remote) return null;
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
  return useMutation({
    mutationFn: ({ mac, cut }: { mac: string; cut: boolean }) =>
      cut ? api.wifiBlock(mac) : api.wifiUnblock(mac),
    onSettled: () =>
      Promise.all([
        qc.invalidateQueries({ queryKey: keys.wifiDenied }),
        qc.invalidateQueries({ queryKey: keys.devices }),
        // Given back: accepted under auto-reject.
        qc.invalidateQueries({ queryKey: keys.autoReject }),
      ]),
  });
}

export function useRename() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ mac, name }: { mac: string; name: string }) =>
      api.rename(mac, name),
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
    onSuccess: (status) => qc.setQueryData(keys.autoReject, status),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.autoReject }),
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

export function useSSIDs() {
  return useQuery({ queryKey: keys.ssids, queryFn: api.ssids });
}

export function useUpdateSSID() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, change }: { id: string; change: WiFiSSIDChange }) =>
      api.updateSSID(id, change),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.ssids }),
  });
}

export function useFilter() {
  return useQuery({ queryKey: keys.filter, queryFn: api.filter });
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
