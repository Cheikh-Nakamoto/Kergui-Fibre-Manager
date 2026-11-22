import { create } from 'zustand';

// The demo has no hub (#43, #46): once its simulated relay is paired, the
// Devices tab shows what remote mode looks like — "via le relais, mis à jour
// il y a…" — clearly marked as the demo, while the list keeps coming from the
// demo box. The owner can rename it (#57), as a real relay's label. In
// memory only: a new launch starts the demo without a relay.

/** The demo relay's agent id, as its rows' test ids show it. */
export const DEMO_RELAY_ID = 'demo';

type State = {
  /** The demo relay's name once paired, null before. */
  relay: string | null;
  /** What the demo relay is, as the owner picked it. */
  kind: string;
  paired: (name: string, kind?: string) => void;
  rename: (name: string) => void;
};

export const useDemoRemote = create<State>((set) => ({
  relay: null,
  kind: 'pc',
  paired: (name, kind = 'pc') => set({ relay: name, kind }),
  rename: (name) => set((s) => (s.relay === null || !name.trim() ? s : { relay: name.trim() })),
}));
