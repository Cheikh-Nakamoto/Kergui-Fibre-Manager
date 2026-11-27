import { create } from 'zustand';

// The in-app signal of a device nobody approved that just joined (#58): the
// Devices tab shows it with « Autoriser », whether or not the phone's
// notifications are on. It goes when the device is approved or dismissed.

export type Arrival = { mac: string; name: string };

type ArrivalsState = {
  items: Arrival[];
  add: (items: Arrival[]) => void;
  /** The device was approved, or the household dismissed the signal. */
  dismiss: (mac: string) => void;
  clear: () => void;
};

export const useArrivals = create<ArrivalsState>((set) => ({
  items: [],
  add: (items) =>
    set((s) => {
      const fresh = items.filter((a) => !s.items.some((b) => b.mac === a.mac));
      return fresh.length ? { items: [...s.items, ...fresh] } : s;
    }),
  dismiss: (mac) => set((s) => ({ items: s.items.filter((a) => a.mac !== mac.toLowerCase()) })),
  clear: () => set({ items: [] }),
}));
