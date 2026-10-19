import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { api, core } from '../core/client';
import type { WiFiSSIDChange } from '../core/types';

// Router writes are never optimistic: the core rereads the box after each
// write, so the lists are refetched from it.

export const keys = {
  devices: ['devices'] as const,
  ssids: ['wifi', 'ssids'] as const,
  filter: ['filter'] as const,
  journal: ['journal'] as const,
};

export function useDevices() {
  return useQuery({ queryKey: keys.devices, queryFn: api.devices, refetchInterval: 30_000 });
}

export function useSetBlocked() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ mac, blocked }: { mac: string; blocked: boolean }) => (blocked ? api.block(mac) : api.unblock(mac)),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.devices }),
  });
}

export function useRename() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ mac, name }: { mac: string; name: string }) => api.rename(mac, name),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.devices }),
  });
}

export function useSSIDs() {
  return useQuery({ queryKey: keys.ssids, queryFn: api.ssids });
}

export function useUpdateSSID() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, change }: { id: string; change: WiFiSSIDChange }) => api.updateSSID(id, change),
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
