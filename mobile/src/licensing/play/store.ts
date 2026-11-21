import * as IAP from 'expo-iap';
import type { ProductSubscription, Purchase, SubscriptionOffer } from 'expo-iap';
import { create } from 'zustand';

import { useAccount } from '../account';
import { HubError } from '../api';
import { useEntitlement } from '../entitlement';
import type { PaidTier } from '../gate';
import { hub } from '../hub';

// The play flavor sells Pro as Google Play Billing subscriptions
// (RFC-006, Two channels). Google charges; the hub is still the authority:
// every purchase token is sent to POST /licensing/v1/play/purchase with the
// box it is for, the hub verifies it with Google, acknowledges it and issues
// the entitlement token. Prices, periods and the trial are set in the Play
// Console and shown as Google formats them, never written in code.

export const PLAY_PRODUCTS: Record<PaidTier, string> = { basic: 'kergui_basic', max: 'kergui_max' };
const SKUS = Object.values(PLAY_PRODUCTS);

export type PlayStatus = 'idle' | 'buying' | 'pending' | 'syncing';

export type PlayProduct = { id: string; price: string | null; trial: boolean };

type State = {
  /** Play Billing answered and the products exist. */
  available: boolean;
  products: Partial<Record<PaidTier, PlayProduct>>;
  status: PlayStatus;
  error: unknown;
  /** A one-line outcome to show (restored, nothing to restore). */
  notice: 'restored' | 'nothingToRestore' | null;
  init: () => Promise<void>;
  buy: (tier: PaidTier) => Promise<void>;
  /** Sends every purchase Play still holds to the hub again. */
  restore: (opts?: { silent?: boolean }) => Promise<void>;
  manage: () => Promise<void>;
};

let products: ProductSubscription[] = [];
let listening = false;

/** The offer to buy: the free trial when this Google account may still use one. */
export function pickOffer(offers: readonly SubscriptionOffer[] | null | undefined): SubscriptionOffer | undefined {
  const withToken = (offers ?? []).filter((o) => !!o.offerTokenAndroid);
  return withToken.find((o) => o.paymentMode === 'free-trial') ?? withToken[0];
}

const ours = (p: Purchase) => SKUS.includes(p.productId);

async function report(p: Purchase) {
  const boxId = useEntitlement.getState().boxId;
  if (!boxId || !p.purchaseToken) throw new HubError(400, 'INVALID_BOX', 'no box to bind the purchase to');
  await hub.playPurchase({ boxId, productId: p.productId, purchaseToken: p.purchaseToken });
  // The hub has acknowledged it with Google; finishing it here too closes
  // the purchase flow on the phone. A second acknowledgement is harmless.
  await IAP.finishTransaction({ purchase: p, isConsumable: false }).catch(() => undefined);
  await useEntitlement.getState().refresh({ force: true });
}

export const usePlay = create<State>((set, get) => {
  const onPurchase = async (p: Purchase) => {
    if (!ours(p)) return;
    if (p.purchaseState === 'pending') {
      // Paid by a slow method: Play confirms later and calls us again.
      set({ status: 'pending' });
      return;
    }
    if (p.purchaseState !== 'purchased') return;
    set({ status: 'syncing', error: null });
    try {
      await report(p);
      set({ status: 'idle' });
    } catch (e) {
      // Not lost: Play keeps the purchase, and restore (or the next launch)
      // sends it again. The hub's own acknowledgement is what keeps Google
      // from refunding it.
      set({ status: e instanceof HubError && e.code === 'PLAY_PURCHASE_PENDING' ? 'pending' : 'idle', error: e });
    }
  };

  const onError = (e: Parameters<Parameters<typeof IAP.purchaseErrorListener>[0]>[0]) => {
    set({ status: 'idle', error: IAP.isUserCancelledError(e) ? null : e });
  };

  return {
    available: false,
    products: {},
    status: 'idle',
    error: null,
    notice: null,

    init: async () => {
      try {
        await IAP.initConnection();
        if (!listening) {
          listening = true;
          IAP.purchaseUpdatedListener((p) => void onPurchase(p));
          IAP.purchaseErrorListener(onError);
        }
        const found = ((await IAP.fetchProducts({ skus: SKUS, type: 'subs' })) ?? []) as ProductSubscription[];
        products = found.filter((p) => SKUS.includes(p.id));
        const view: Partial<Record<PaidTier, PlayProduct>> = {};
        for (const tier of Object.keys(PLAY_PRODUCTS) as PaidTier[]) {
          const p = products.find((x) => x.id === PLAY_PRODUCTS[tier]);
          if (p) {
            view[tier] = {
              id: p.id,
              price: p.displayPrice ?? null,
              trial: !!p.subscriptionOffers?.some((o) => o.paymentMode === 'free-trial'),
            };
          }
        }
        set({ available: products.length > 0, products: view });
        // The hub wants to hear about purchases on launch too (restoring
        // them on a new phone, or one whose report failed).
        await get().restore({ silent: true });
      } catch {
        // No Play (emulator without it, no Google account): say purchases
        // are unavailable; the token already held still works.
        set({ available: false });
      }
    },

    buy: async (tier) => {
      const id = PLAY_PRODUCTS[tier];
      const product = products.find((p) => p.id === id);
      const account = useAccount.getState().account;
      if (!product || !account) return;
      set({ status: 'buying', error: null, notice: null });
      const offer = pickOffer(product.subscriptionOffers);
      try {
        // Basic -> Max (or back) replaces the running subscription instead
        // of adding a second one.
        const held = (await IAP.getAvailablePurchases()).find((p) => ours(p) && p.productId !== id && p.purchaseToken);
        await IAP.requestPurchase({
          type: 'subs',
          request: {
            apple: { sku: id },
            google: {
              skus: [id],
              subscriptionOffers: offer?.offerTokenAndroid ? [{ sku: id, offerToken: offer.offerTokenAndroid }] : null,
              // The hub refuses a purchase token bought under another account.
              obfuscatedAccountId: account.accountId,
              ...(held
                ? {
                    purchaseToken: held.purchaseToken,
                    subscriptionProductReplacementParams: { oldProductId: held.productId, replacementMode: 'with-time-proration' as const },
                  }
                : {}),
            },
          },
        });
        // The outcome arrives through the listeners above.
      } catch (e) {
        set({ status: 'idle', error: IAP.isUserCancelledError(e) ? null : e });
      }
    },

    restore: async ({ silent = false } = {}) => {
      if (!useAccount.getState().account || !useEntitlement.getState().boxId) return;
      if (!silent) set({ status: 'syncing', error: null, notice: null });
      try {
        const held = (await IAP.getAvailablePurchases()).filter((p) => ours(p) && p.purchaseState === 'purchased');
        let first: unknown = null;
        for (const p of held) {
          try {
            await report(p);
          } catch (e) {
            first ??= e;
          }
        }
        if (!silent) set({ status: 'idle', error: first, notice: first ? null : held.length ? 'restored' : 'nothingToRestore' });
      } catch (e) {
        if (!silent) set({ status: 'idle', error: e });
      }
    },

    manage: async () => {
      try {
        const sub = useEntitlement.getState().claims?.tier;
        await IAP.deepLinkToSubscriptions({ skuAndroid: sub ? PLAY_PRODUCTS[sub] : SKUS[0] });
      } catch (e) {
        set({ error: e });
      }
    },
  };
});
