import * as Crypto from 'expo-crypto';
import { File, Paths } from 'expo-file-system';
import * as SecureStore from 'expo-secure-store';
import { create } from 'zustand';

import KerguiCore from '../../modules/kergui-core';
import { useInbox } from '../alerts/inbox';
import type { ReleaseUpdate } from '../licensing/api';
import { config } from '../licensing/config';
import { hub } from '../licensing/hub';

// Self-update of the website APK (#65, #74). The hub says whether a newer
// published version exists and the minimum build; this module fetches it and
// hands it to Android's installer, which always asks the household to confirm.
//
//   - Only the website APK: its native flag (plugins/withSelfUpdate.js) is
//     absent from the Play bundle, and an app the Play Store installed is
//     never offered anything.
//   - Checked at launch and when the app comes back to the front, at most
//     every CHECK_EVERY (30 minutes after a failure).
//   - Wi-Fi: downloaded in the background, then offered as « Mise à jour
//     prête ». Mobile data: offered with its size, downloaded on tap.
//   - « Plus tard » hides the offer until the next launch.
//   - Below the minimum build: a blocking screen (UpdateHost).
//   - The file's size and SHA-256 are checked before it is offered and
//     again right before it goes to the installer.

export const CHECK_EVERY = 6 * 60 * 60 * 1000;
export const RETRY_AFTER = 30 * 60 * 1000;
export const PLAY_STORE = 'com.android.vending';
export const DOWNLOAD_PAGE = 'https://kergui.click/telecharger';
const ID_KEY = 'kergui.installationId';
const APK_NAME = 'kergui-update.apk';

/**
 * none: nothing to offer; offer: known, not downloaded (mobile data);
 * downloading; ready: downloaded and verified; failed: the download did not
 * finish or the file was wrong (retried at the next check).
 */
export type UpdatePhase = 'none' | 'offer' | 'downloading' | 'ready' | 'failed';
/** asked: the settings page was opened; refused: still not granted on return. */
export type PermissionStep = 'explain' | 'asked' | 'refused' | null;

type UpdateState = {
  phase: UpdatePhase;
  release: ReleaseUpdate | null;
  /** This build is below the hub's minimum: the blocking screen. */
  required: boolean;
  /** « Plus tard » was pressed since this launch. */
  dismissed: boolean;
  /** The household started this download (mobile data): show its progress. */
  byTap: boolean;
  failure: 'network' | 'corrupt' | null;
  permission: PermissionStep;
  nextCheckAt: number;
  /** At launch (`launch`) and on return to the front. */
  check: (opts?: { launch?: boolean }) => Promise<void>;
  /** « Mettre à jour »: downloads when needed, then installs. */
  update: () => Promise<void>;
  later: () => void;
  /** Opens the « install unknown apps » page, after the explanation. */
  openSettings: () => Promise<void>;
  /** Back in the app after the settings page: installs when granted. */
  resume: () => Promise<void>;
};

/** Whether this build may update itself, and its build number; null when not. */
export function selfUpdateTarget(): { build: number } | null {
  if (!config.hubUrl) return null;
  const info = KerguiCore.selfUpdateInfo?.();
  if (!info?.allowed || info.installer === PLAY_STORE) return null;
  const build = Math.trunc(Number(info.build));
  return build > 0 ? { build } : null;
}

async function installationId(): Promise<string> {
  const stored = await SecureStore.getItemAsync(ID_KEY).catch(() => null);
  if (stored && /^[A-Za-z0-9_-]{8,64}$/.test(stored)) return stored;
  const id = Crypto.randomUUID();
  await SecureStore.setItemAsync(ID_KEY, id).catch(() => undefined);
  return id;
}

function apkFile(): File {
  return new File(Paths.cache, APK_NAME);
}

function discard() {
  try {
    const f = apkFile();
    if (f.exists) f.delete();
  } catch {
    // Nothing to remove.
  }
}

/** The downloaded file is exactly the one the hub declared. */
async function verified(u: ReleaseUpdate): Promise<boolean> {
  try {
    const f = apkFile();
    if (!f.exists || f.size !== u.size) return false;
    const sha = await KerguiCore.sha256File?.(f.uri);
    return !!sha && sha.toLowerCase() === u.sha256.toLowerCase();
  } catch {
    return false;
  }
}

/** One « update » entry in the notifications, for this version only. */
function signal(u: ReleaseUpdate | null) {
  const inbox = useInbox.getState();
  const id = u ? `update:${u.build}` : '';
  for (const e of inbox.items) if (e.kind === 'update' && e.id !== id) inbox.remove(e.id);
  if (u) inbox.add([{ id, kind: 'update', mac: '', name: u.versionName, text: u.notes || undefined, at: Date.now() }]);
}

let checking = false;

export const useUpdate = create<UpdateState>((set, get) => {
  async function download(byTap: boolean): Promise<boolean> {
    const u = get().release;
    if (!u || get().phase === 'downloading') return false;
    set({ phase: 'downloading', failure: null, byTap });
    try {
      discard();
      await File.downloadFileAsync(u.url, apkFile(), { idempotent: true });
    } catch {
      discard();
      set({ phase: 'failed', failure: 'network', nextCheckAt: Date.now() + RETRY_AFTER });
      return false;
    }
    if (!(await verified(u))) {
      discard();
      set({ phase: 'failed', failure: 'corrupt', nextCheckAt: Date.now() + RETRY_AFTER });
      return false;
    }
    set({ phase: 'ready', failure: null });
    signal(u);
    return true;
  }

  async function install() {
    const u = get().release;
    if (!u) return;
    if (!(await verified(u))) {
      discard();
      set({ phase: 'failed', failure: 'corrupt', permission: null, nextCheckAt: Date.now() + RETRY_AFTER });
      return;
    }
    if (!KerguiCore.canInstallPackages?.()) {
      // Said before Android asks (story 12); refused once asked (story 13).
      set({ permission: get().permission === 'asked' ? 'refused' : 'explain' });
      return;
    }
    set({ permission: null });
    await KerguiCore.installApk?.(apkFile().uri);
  }

  return {
    phase: 'none',
    release: null,
    required: false,
    dismissed: false,
    byTap: false,
    failure: null,
    permission: null,
    nextCheckAt: 0,

    check: async (opts = {}) => {
      const target = selfUpdateTarget();
      if (!target) return;
      if (opts.launch) set({ dismissed: false });
      if (checking || get().phase === 'downloading') return;
      if (!opts.launch && Date.now() < get().nextCheckAt) return;
      checking = true;
      try {
        const answer = await hub.checkRelease({
          platform: 'android',
          channel: 'website',
          build: target.build,
          installationId: await installationId(),
        });
        const u = answer.update && answer.update.build > target.build ? answer.update : null;
        set({ required: !!answer.required, nextCheckAt: Date.now() + CHECK_EVERY });
        if (!u) {
          discard();
          set({ release: null, phase: 'none', failure: null });
          signal(null);
          return;
        }
        const same = get().release?.build === u.build;
        set({ release: u });
        if (same && get().phase === 'ready') return;
        if (await verified(u)) {
          set({ phase: 'ready', failure: null });
          signal(u);
          return;
        }
        if (KerguiCore.onUnmeteredNetwork?.()) {
          await download(false);
          return;
        }
        set({ phase: 'offer', failure: null });
        signal(u);
      } catch {
        // The hub is out of reach: try again later, nothing to say.
        set({ nextCheckAt: Date.now() + RETRY_AFTER });
      } finally {
        checking = false;
      }
    },

    update: async () => {
      const { phase } = get();
      if (phase === 'downloading') return;
      if (phase !== 'ready' && !(await download(true))) return;
      await install();
    },

    later: () => set({ dismissed: true, permission: null }),

    openSettings: async () => {
      set({ permission: 'asked' });
      try {
        await KerguiCore.openInstallSettings?.();
      } catch {
        set({ permission: 'refused' });
      }
    },

    resume: async () => {
      if (get().permission !== 'asked' || get().phase !== 'ready') return;
      await install();
    },
  };
});
