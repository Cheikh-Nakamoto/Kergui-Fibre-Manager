import { create } from 'zustand';

import { remoteCore } from '../core/remote';
import { HubError, type WhatsAppInvite, type WhatsAppProvider } from '../licensing/api';
import { hub } from '../licensing/hub';
import { withHousehold } from '../licensing/household';
import { changeBundle } from './delegations';

// The household's alert channels besides this phone (issue #49, RFC-005
// Alerts). WhatsApp: the person writes first — the app asks the hub for a
// wa.me link that pre-fills "KERGUI <code>", WhatsApp opens, they send it,
// and the hub answers in the conversation. The provider's licence notice
// (if any) is shown where the link is. The hub key: the owner's
// "Répondre 1 pour bloquer" switch signs a hub_block delegation to the hub's
// narrow key into the bundle, or revokes it; the relays read the bundle and
// refuse the key whatever the hub does once it is off.

export const alertChannelsKey = ['hub', 'alert-channels'] as const;

/**
 * on / off: the owner may switch. unavailable: the hub has no key seed.
 * no_relay: nothing could act on a reply yet. not_owner: only the owner's
 * phone signs the bundle.
 */
export type HubKeyState = { state: 'on' | 'off' | 'unavailable' | 'no_relay' | 'not_owner'; keyId?: string };

export type AlertChannelsView = {
  provider: WhatsAppProvider;
  /** This member's own number receives the alerts. */
  linked: boolean;
  /** How many numbers of the household receive them. */
  linkedCount: number;
  /** Owners and admins may link a number; the hub refuses a viewer. */
  canLink: boolean;
  hubKey: HubKeyState;
};

export function loadAlertChannels(): Promise<AlertChannelsView> {
  return withHousehold(async (tok, tenantId) => {
    const [status, members] = await Promise.all([hub.whatsapp(tok, tenantId), hub.members(tok, tenantId)]);
    const me = members.you;
    const active = status.optIns.filter((o) => o.active);
    const view = {
      provider: status.provider,
      linked: active.some((o) => o.userId === me.userId),
      linkedCount: active.length,
      canLink: me.role === 'owner' || me.role === 'admin',
    };
    if (me.role !== 'owner') return { ...view, hubKey: { state: 'not_owner' } };
    const key = await hub.hubKey(tok, tenantId);
    if (!key.enabled || !key.keyId) return { ...view, hubKey: { state: 'unavailable' } };
    const bundle = await hub.trustBundle(tok, tenantId);
    const relays = bundle ? await remoteCore.relays(tenantId, bundle) : null;
    if (relays?.owner && relays.hubKey?.keyId === key.keyId) return { ...view, hubKey: { state: 'on', keyId: key.keyId } };
    if (!relays?.owner || relays.relays.length === 0) return { ...view, hubKey: { state: 'no_relay', keyId: key.keyId } };
    return { ...view, hubKey: { state: 'off', keyId: key.keyId } };
  });
}

export const whatsapp = {
  /** The wa.me link for this member's number; WhatsApp is opened by the screen. */
  invite: (): Promise<WhatsAppInvite> => withHousehold((tok, tenantId) => hub.whatsappOptIn(tok, tenantId)),
  /** "Répondre 1 pour bloquer": delegate the hub key in the bundle, or revoke it. */
  setHubKey: async (on: boolean): Promise<void> => {
    const key = await withHousehold((tok, tenantId) => hub.hubKey(tok, tenantId));
    if (!key.enabled || !key.keyId || !key.ed25519 || !key.x25519) {
      throw new HubError(409, 'NOT_CONFIGURED', 'this hub has no key to block by message');
    }
    await changeBundle(on ? { hubKey: { ed25519: key.ed25519, x25519: key.x25519, label: 'WhatsApp' } } : { revoke: key.keyId });
  },
};

// The demo has no hub: the provider is the one a pilot hub runs, the link
// is shown rather than opened (WhatsApp would leave the demo), and the
// switch lives in memory.

/** What a hub on whatsmeow answers (internal/hub/alerts/adapters/whatsmeow): no notice to show. */
export const DEMO_PROVIDER: WhatsAppProvider = { name: 'whatsmeow' };

type DemoState = {
  linked: boolean;
  hubKey: boolean;
  /** The message the wa.me link would pre-fill. */
  message: string;
  link: () => void;
  setHubKey: (on: boolean) => void;
};

export const useDemoWhatsApp = create<DemoState>((set) => ({
  linked: false,
  hubKey: false,
  message: '',
  link: () => set({ linked: true, message: 'KERGUI DEMO7K2P' }),
  setHubKey: (on) => set({ hubKey: on }),
}));
