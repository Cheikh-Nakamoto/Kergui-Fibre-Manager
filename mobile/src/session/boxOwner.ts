import { useAccount } from '../licensing/account';
import { household } from '../licensing/household';

/**
 * Who this phone is for the box (#93): the owner sets a new box's first
 * password and sees it in Réglages; a member is sent to the owner, with the
 * owner's number (digits only, for a wa.me link) when the hub shows it.
 */
export type BoxRole = { owner: true } | { owner: false; ownerPhone: string | null };

/**
 * A phone with no Kergui account, or no household, is the one installing
 * the box: the owner. A hub that cannot be reached says the same, because a
 * new box often has no Internet yet and whoever holds the label goes on.
 */
export async function boxRole(): Promise<BoxRole> {
  const account = useAccount.getState();
  if (!account.account || !account.household()) return { owner: true };
  let answer;
  try {
    answer = await household.members();
  } catch {
    return { owner: true };
  }
  if (answer.you?.role === 'owner') return { owner: true };
  const phone = answer.members?.find((m) => m.role === 'owner')?.phone ?? '';
  // A masked number (« +221 77 *** ** 01 ») cannot be dialled.
  const digits = phone.includes('*') ? '' : phone.replace(/\D/g, '');
  return { owner: false, ownerPhone: digits || null };
}
