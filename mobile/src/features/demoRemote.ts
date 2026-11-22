import { create } from 'zustand';

// The demo has no hub (#43, #46): once its simulated relay is paired, the
// Devices tab shows what remote mode looks like — "via le relais, mis à jour
// il y a…" — clearly marked as the demo, while the list keeps coming from the
// demo box. In memory only: a new launch starts the demo without a relay.

type State = {
  /** The demo relay's name once paired, null before. */
  relay: string | null;
  paired: (name: string) => void;
};

export const useDemoRemote = create<State>((set) => ({
  relay: null,
  paired: (name) => set({ relay: name }),
}));
