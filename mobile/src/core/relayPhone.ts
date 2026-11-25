import { PermissionsAndroid, Platform, type Permission } from 'react-native';

import KerguiCore from '../../modules/kergui-core';
import { t } from '../i18n';

// This phone as the household's relay (issue #45, RFC-005). The pairing is
// the core's (RelayEnroll, RelayCheckTrust: the keys, the hub token and,
// later, the sealed box password stay in its vault); running it is the
// Android foreground service RelayService, which runs internal/agent's loop
// in the core. JS only words what the core answers.

/** What the relay is doing, as the core says it. */
export type RelayState = 'stopped' | 'starting' | 'active' | 'paused' | 'standby' | 'offline' | 'wrong-network';

export type RelayStatus = {
  running: boolean;
  state: RelayState;
  /** 'signature': the Wi-Fi and the box were checked; 'box-identity': the box alone (no Wi-Fi name). */
  check: '' | 'signature' | 'box-identity';
  autoReject: boolean;
};

/** What the relay phone shows after redeeming the QR. */
export type Enrolled = { agentId: string; tenantId: string; code: string };

export class RelayPhoneError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'RelayPhoneError';
  }
}

const STATES: RelayState[] = ['stopped', 'starting', 'active', 'paused', 'standby', 'offline', 'wrong-network'];

/** The owner's QR payload: kgp1.<code>.<fingerprint>. */
const INVITE = /^kgp1\.[A-Za-z2-7]+\.[0-9A-Za-z_-]{8,}$/;

/** The QR or the code typed under it, cleaned of spaces; null when it is not one. */
export function parseInvite(raw: string): string | null {
  const v = raw.replace(/\s+/g, '');
  return INVITE.test(v) ? v : null;
}

function parse(raw: string): Record<string, unknown> {
  try {
    const o: unknown = JSON.parse(raw);
    if (o && typeof o === 'object') return o as Record<string, unknown>;
  } catch {
    // fall through
  }
  throw new RelayPhoneError('the core answered something that is not JSON');
}

export function readStatus(raw: string): RelayStatus {
  const o = parse(raw);
  const state = STATES.includes(o.state as RelayState) ? (o.state as RelayState) : 'stopped';
  const check = o.check === 'signature' || o.check === 'box-identity' ? o.check : '';
  return { running: o.running === true, state, check, autoReject: o.autoReject === true };
}

export const relayPhone = {
  /** Only Android runs the relay service today. */
  supported: (): boolean => Platform.OS === 'android' && typeof KerguiCore.startRelay === 'function',

  enroll: async (hubUrl: string, invite: string, name: string): Promise<Enrolled> => {
    if (!KerguiCore.relayEnroll) throw new RelayPhoneError('this build cannot pair a relay');
    const o = parse(await KerguiCore.relayEnroll(hubUrl, invite, name));
    const code = o.code;
    if (typeof code !== 'string' || !/^\d{6}$/.test(code)) throw new RelayPhoneError('the core did not answer six digits');
    return { agentId: String(o.agentId ?? ''), tenantId: String(o.tenantId ?? ''), code };
  },

  enrolled: async (): Promise<boolean> => (KerguiCore.relayEnrolled ? KerguiCore.relayEnrolled() : false),

  /** Whether the owner confirmed the six digits and signed this phone in. */
  trusted: async (): Promise<boolean> => {
    if (!KerguiCore.relayCheckTrust) return false;
    return parse(await KerguiCore.relayCheckTrust()).trusted === true;
  },

  status: async (): Promise<RelayStatus> =>
    KerguiCore.relayStatus ? readStatus(await KerguiCore.relayStatus()) : { running: false, state: 'stopped', check: '', autoReject: false },

  /** Whether the service is on (it restarts at boot). */
  enabled: (): boolean => KerguiCore.relayEnabled?.() ?? false,

  /**
   * The two permissions the relay asks for: notifications (Android 13+),
   * for its ongoing notification, and the precise location, without which
   * Android does not give the Wi-Fi's name. Refusing either still relays.
   */
  askPermissions: async (): Promise<{ location: boolean; notifications: boolean }> => {
    if (Platform.OS !== 'android') return { location: false, notifications: false };
    const P = PermissionsAndroid.PERMISSIONS;
    const wanted: Permission[] = [P.ACCESS_FINE_LOCATION, P.ACCESS_COARSE_LOCATION];
    if (Number(Platform.Version) >= 33 && P.POST_NOTIFICATIONS) wanted.push(P.POST_NOTIFICATIONS);
    const got = await PermissionsAndroid.requestMultiple(wanted);
    const ok = (p: string | undefined) => !!p && got[p as keyof typeof got] === PermissionsAndroid.RESULTS.GRANTED;
    return {
      location: ok(P.ACCESS_FINE_LOCATION),
      notifications: Number(Platform.Version) < 33 || ok(P.POST_NOTIFICATIONS),
    };
  },

  /** Whether the Wi-Fi's name can be read (precise location granted). */
  locationGranted: async (): Promise<boolean> =>
    Platform.OS === 'android' && PermissionsAndroid.check(PermissionsAndroid.PERMISSIONS.ACCESS_FINE_LOCATION),

  start: async (): Promise<void> => {
    if (!KerguiCore.startRelay) throw new RelayPhoneError('this build has no relay service');
    const { vaultKey } = await import('../session/store');
    await KerguiCore.startRelay({
      vaultKey: await vaultKey(),
      title: t('relayPhone.notifTitle'),
      channel: t('relayPhone.notifChannel'),
      stopText: t('relayPhone.notifStop'),
      startingText: t('relayPhone.notifStarting'),
      activeText: t('relayPhone.notifActive'),
      activeBoxOnlyText: t('relayPhone.notifActiveBoxOnly'),
      standbyText: t('relayPhone.notifStandby'),
      pausedText: t('relayPhone.notifPaused'),
      wrongNetworkText: t('relayPhone.notifWrongNetwork'),
      offlineText: t('relayPhone.notifOffline'),
      noWifiText: t('relayPhone.notifNoWifi'),
      noBoxText: t('relayPhone.notifNoBox'),
    });
  },

  stop: async (): Promise<void> => {
    await KerguiCore.stopRelay?.();
  },
};
