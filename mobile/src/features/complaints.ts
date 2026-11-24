import { core } from '../core/client';
import { logLine } from '../lib/format';
import { type ComplaintCategory, type ComplaintReceipt, type Complaint, HubError } from '../licensing/api';
import { config } from '../licensing/config';
import { hub } from '../licensing/hub';
import { useAccount } from '../licensing/account';

// « Signaler un problème » (issue #84, parent #68): the household tells
// Kergui what is wrong — a category, a message, and the app's journal only
// when they tick the box. The hub (internal/hub/complaints) answers with a
// complaint number at once; the team's answers come back as a push the app
// files in its notifications (kind « reply »), where the customer can write
// again on the same complaint.

/** In the order the screen shows them. */
export const COMPLAINT_CATEGORIES: readonly ComplaintCategory[] = ['paiement', 'box', 'application', 'autre'];

/** A message of the thread, in characters (the hub's MaxMessage). */
export const COMPLAINT_MAX_MESSAGE = 2000;
/** The journal sent, in bytes: the hub keeps 64 KiB, the recent end. */
export const COMPLAINT_MAX_JOURNAL = 64 << 10;

/** Why the app cannot send: no hub in this build, the demo, nobody signed in. */
export type ComplaintBlock = 'no_hub' | 'demo' | 'signed_out';

/** What stops this app from sending a complaint now, or null when it can. */
export function complaintBlock(demo: boolean): ComplaintBlock | null {
  if (!config.hubUrl) return 'no_hub';
  if (demo) return 'demo';
  if (!useAccount.getState().account) return 'signed_out';
  return null;
}

/** The core's journal as text, the most recent end within the hub's cap. */
export async function journalText(): Promise<string> {
  const lines = (await core.journal()).map(logLine);
  // Bytes, not characters: the hub caps bytes. Whole lines, newest kept.
  const kept: string[] = [];
  let bytes = 0;
  for (let i = lines.length - 1; i >= 0; i--) {
    const n = size(lines[i]) + (kept.length ? 1 : 0);
    if (bytes + n > COMPLAINT_MAX_JOURNAL) break;
    kept.push(lines[i]);
    bytes += n;
  }
  return kept.reverse().join('\n');
}

/** UTF-8 length of s, in bytes. */
function size(s: string): number {
  let n = 0;
  for (const ch of s) {
    const c = ch.codePointAt(0)!;
    n += c < 0x80 ? 1 : c < 0x800 ? 2 : c < 0x10000 ? 3 : 4;
  }
  return n;
}

export type NewComplaint = { category: ComplaintCategory | null; message: string; withJournal: boolean };

/** A refusal raised by the app itself, before the hub is called. */
export class ComplaintInputError extends Error {
  constructor(readonly reason: 'category' | 'message' | 'too_long') {
    super(reason);
    this.name = 'ComplaintInputError';
  }
}

export const complaints = {
  /** Sends a new complaint; the journal goes only when `withJournal`. */
  send: async (c: NewComplaint): Promise<ComplaintReceipt> => {
    if (!c.category) throw new ComplaintInputError('category');
    const message = c.message.trim();
    if (!message) throw new ComplaintInputError('message');
    if ([...message].length > COMPLAINT_MAX_MESSAGE) throw new ComplaintInputError('too_long');
    const journal = c.withJournal ? await journalText().catch(() => '') : '';
    return call(() =>
      hub.createComplaint({ category: c.category!, message, source: 'app', ...(journal ? { journal } : {}) }),
    );
  },
  /** The customer's answer on the thread of complaint n° `number`. */
  reply: async (number: number, text: string): Promise<Complaint> => {
    const message = text.trim();
    if (!message) throw new ComplaintInputError('message');
    if ([...message].length > COMPLAINT_MAX_MESSAGE) throw new ComplaintInputError('too_long');
    return call(() => hub.replyComplaint(number, message));
  },
};

/** A refused session signs the account out, as the other licensing calls do. */
async function call<T>(run: () => Promise<T>): Promise<T> {
  try {
    return await run();
  } catch (e) {
    if (e instanceof HubError && e.sessionInvalid) await useAccount.getState().invalidate();
    throw e;
  }
}
