import * as IAP from 'expo-iap';
import type { ProductSubscription, Purchase, PurchaseError, SubscriptionOffer } from 'expo-iap';
import * as SecureStore from 'expo-secure-store';
import { create } from 'zustand';

// Kergui Premium is one auto-renewing subscription sold through the store
// (Google Play Billing / App Store). There is no server: the store is the
// source of truth, and the last answer is cached so the app works on a home
// Wi-Fi whose Internet is down. Price, period and the free trial are set in
// the Play Console / App Store Connect, never in code.
export const PREMIUM_SKU = 'kergui_premium';

const CACHE_KEY = 'kergui.premium';

export type BillingStatus = 'idle' | 'buying' | 'pending';

type State = {
  /** Unlocked, as last confirmed by the store (or cached from then). */
  premium: boolean;
  /** The store answered: purchases can be made. */
  available: boolean;
  /** Localised price of the subscription, as the store shows it ("1 000 F CFA"). */
  price: string | null;
  /** A free-trial offer is available to this account. */
  trial: boolean;
  status: BillingStatus;
  error: string | null;
  init: () => Promise<void>;
  refresh: () => Promise<void>;
  buy: () => Promise<void>;
  restore: () => Promise<void>;
  manage: () => Promise<void>;
};

let product: ProductSubscription | null = null;
let listening = false;

function message(e: unknown): string {
  if (e && typeof e === 'object' && 'message' in e) return String((e as { message: unknown }).message);
  return String(e);
}

/** The offer to buy on Google Play: the free trial when this account may still use one. */
export function pickOffer(offers: readonly SubscriptionOffer[] | null | undefined): SubscriptionOffer | undefined {
  const withToken = (offers ?? []).filter((o) => !!o.offerTokenAndroid);
  return withToken.find((o) => o.paymentMode === 'free-trial') ?? withToken[0];
}

async function remember(premium: boolean) {
  await SecureStore.setItemAsync(CACHE_KEY, premium ? '1' : '0');
}

export const useBilling = create<State>((set, get) => {
  const unlock = async () => {
    set({ premium: true, status: 'idle', error: null });
    await remember(true);
  };

  const onPurchase = async (p: Purchase) => {
    if (p.productId !== PREMIUM_SKU) return;
    if (p.purchaseState === 'pending') {
      // Paid by a slow method: the store confirms later and calls us again.
      set({ status: 'pending' });
      return;
    }
    if (p.purchaseState !== 'purchased') return;
    // Google refunds a purchase that is not acknowledged within 3 days.
    await IAP.finishTransaction({ purchase: p, isConsumable: false });
    await unlock();
  };

  const onError = (e: PurchaseError) => {
    set({ status: 'idle', error: IAP.isUserCancelledError(e) ? null : message(e) });
  };

  return {
    premium: false,
    available: false,
    price: null,
    trial: false,
    status: 'idle',
    error: null,

    init: async () => {
      if ((await SecureStore.getItemAsync(CACHE_KEY)) === '1') set({ premium: true });
      try {
        await IAP.initConnection();
        if (!listening) {
          listening = true;
          IAP.purchaseUpdatedListener((p) => void onPurchase(p).catch((e) => set({ status: 'idle', error: message(e) })));
          IAP.purchaseErrorListener(onError);
        }
        const found = (await IAP.fetchProducts({ skus: [PREMIUM_SKU], type: 'subs' })) ?? [];
        product = (found as ProductSubscription[]).find((p) => p.id === PREMIUM_SKU) ?? null;
        set({
          available: !!product,
          price: product?.displayPrice ?? null,
          trial: !!product?.subscriptionOffers?.some((o) => o.paymentMode === 'free-trial'),
        });
        await get().refresh();
      } catch {
        // No store (emulator without Play, no account, offline): keep the
        // cached answer. The screens say purchases are unavailable.
        set({ available: false });
      }
    },

    refresh: async () => {
      // A purchase made while the app was killed may still be waiting for
      // its acknowledgement: finish it before asking what is active.
      const pending = await IAP.getAvailablePurchases();
      for (const p of pending) {
        if (p.productId === PREMIUM_SKU && p.purchaseState === 'purchased' && 'isAcknowledgedAndroid' in p && p.isAcknowledgedAndroid === false) {
          await IAP.finishTransaction({ purchase: p, isConsumable: false });
        }
      }
      const active = await IAP.getActiveSubscriptions([PREMIUM_SKU]);
      const premium = active.some((s) => s.productId === PREMIUM_SKU && s.isActive);
      set({ premium });
      await remember(premium);
    },

    buy: async () => {
      if (!product) {
        set({ error: null, available: false });
        return;
      }
      set({ status: 'buying', error: null });
      const offer = pickOffer(product.subscriptionOffers);
      try {
        await IAP.requestPurchase({
          type: 'subs',
          request: {
            apple: { sku: PREMIUM_SKU },
            google: {
              skus: [PREMIUM_SKU],
              subscriptionOffers: offer?.offerTokenAndroid ? [{ sku: PREMIUM_SKU, offerToken: offer.offerTokenAndroid }] : null,
            },
          },
        });
        // The outcome arrives through the listeners above.
      } catch (e) {
        set({ status: 'idle', error: IAP.isUserCancelledError(e) ? null : message(e) });
      }
    },

    restore: async () => {
      set({ error: null });
      try {
        await IAP.restorePurchases();
        await get().refresh();
      } catch (e) {
        set({ error: message(e) });
      }
    },

    manage: async () => {
      try {
        await IAP.deepLinkToSubscriptions({ skuAndroid: PREMIUM_SKU });
      } catch (e) {
        set({ error: message(e) });
      }
    },
  };
});
