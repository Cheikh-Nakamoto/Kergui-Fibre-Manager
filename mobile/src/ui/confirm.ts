import { Alert } from 'react-native';
import { create } from 'zustand';

import { t } from '../i18n';

/**
 * One confirmation (#119, audit H7): the title names the action (« Couper
 * Internet pour toute la maison ? »), the message is one line of
 * consequence, the button says the action. Destructive: a red filled
 * button, with its icon.
 */
export type ConfirmRequest = {
  title?: string;
  message: string;
  confirmLabel?: string;
  destructive?: boolean;
  /** A Material Community icon for the action button. */
  icon?: string;
};

type Shown = Required<Pick<ConfirmRequest, 'message' | 'confirmLabel'>> & Omit<ConfirmRequest, 'message' | 'confirmLabel'>;

type State = {
  /** The confirmation on screen, or null. */
  shown: Shown | null;
  /** How many ConfirmHost are mounted: none means the system alert. */
  hosts: number;
};

export const useConfirm = create<State>(() => ({ shown: null, hosts: 0 }));

let pending: ((ok: boolean) => void) | null = null;

/** The host's answer: true for the action, false for « Annuler » or a dismiss. */
export function answerConfirm(ok: boolean) {
  const resolve = pending;
  pending = null;
  useConfirm.setState({ shown: null });
  resolve?.(ok);
}

/** ConfirmHost registers itself while it is mounted; returns the unregister. */
export function registerConfirmHost(): () => void {
  useConfirm.setState((s) => ({ hosts: s.hosts + 1 }));
  return () => useConfirm.setState((s) => ({ hosts: Math.max(0, s.hosts - 1) }));
}

/**
 * Asks before a write (the CLI's --yes): resolves true when the action
 * button is pressed. The dialog is a Paper Dialog drawn by the ConfirmHost of
 * the root layout, because Android ignores the system alert's « destructive »
 * style. With no host mounted (a background task), the system alert asks.
 * The former call, confirm(message, label, destructive), still works.
 */
export function confirm(request: ConfirmRequest | string, confirmLabel?: string, destructive = false): Promise<boolean> {
  const req: ConfirmRequest = typeof request === 'string' ? { message: request, confirmLabel, destructive } : request;
  const shown: Shown = { ...req, confirmLabel: req.confirmLabel ?? t('common.confirm') };
  if (useConfirm.getState().hosts === 0) return systemAlert(shown);
  // A second question answers the first one « no »: one dialog at a time.
  if (pending) answerConfirm(false);
  return new Promise((resolve) => {
    pending = resolve;
    useConfirm.setState({ shown });
  });
}

function systemAlert(req: Shown): Promise<boolean> {
  return new Promise((resolve) => {
    Alert.alert(req.title ?? '', req.message, [
      { text: t('common.cancel'), style: 'cancel', onPress: () => resolve(false) },
      { text: req.confirmLabel, style: req.destructive ? 'destructive' : 'default', onPress: () => resolve(true) },
    ]);
  });
}
