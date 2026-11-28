import { create } from 'zustand';

/**
 * The bottom panel of the Wi-Fi's card (« Gérer », #108), opened again after
 * « Voici les appareils ». A Paper Portal: drawn above every tab, so a way
 * out of it to another tab closes it first (#134).
 */
export const useLockPanel = create<{ open: boolean; show: () => void; hide: () => void }>((set) => ({
  open: false,
  show: () => set({ open: true }),
  hide: () => set({ open: false }),
}));
