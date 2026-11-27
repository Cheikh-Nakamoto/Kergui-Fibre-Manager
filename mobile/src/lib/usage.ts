import type { Device, Usage } from '../core/types';
import { displayName, isHidden, matchesDevice } from './format';

/** One line of the Conso tab. */
export type UsageRow = {
  mac: string;
  name: string;
  /** The device as the box lists it, when it still does. */
  device?: Device;
  bytes: number;
  sent: number;
  received: number;
  /** Part of the busiest device's consumption, 0..1: the length of its bar. */
  share: number;
};

const norm = (m: string) => m.toLowerCase();

/**
 * The month's consumption per device, most first. Every device the box lists
 * is there (at 0 when nothing was measured), plus the ones that consumed and
 * are no longer listed, by MAC. query is the device list's search; the bars
 * stay relative to the whole house.
 *
 * This phone and the owner's phones are left out, as in the device list
 * (isHidden): their sheet is not one to block from (#113).
 */
export function usageRows(devices: Device[], usage: Usage[], query: string): UsageRow[] {
  const hidden = new Set(devices.filter(isHidden).map((d) => norm(d.MAC)));
  const byMac = new Map<string, UsageRow>();
  for (const d of devices) {
    if (hidden.has(norm(d.MAC))) continue;
    byMac.set(norm(d.MAC), { mac: d.MAC, name: displayName(d), device: d, bytes: 0, sent: 0, received: 0, share: 0 });
  }
  for (const u of usage) {
    if (hidden.has(norm(u.mac))) continue;
    const row = byMac.get(norm(u.mac)) ?? { mac: u.mac, name: u.mac, bytes: 0, sent: 0, received: 0, share: 0 };
    row.bytes += u.bytes;
    row.sent += u.sent ?? 0;
    row.received += u.received ?? 0;
    byMac.set(norm(u.mac), row);
  }
  const rows = [...byMac.values()];
  const top = Math.max(0, ...rows.map((r) => r.bytes));
  for (const r of rows) r.share = top > 0 ? r.bytes / top : 0;
  const match = (r: UsageRow) =>
    matchesDevice(r.device ?? { CustomName: '', Hostname: '', MAC: r.mac, IP: '', Vendor: '' }, query);
  return rows.filter(match).sort((a, b) => b.bytes - a.bytes || a.name.localeCompare(b.name, 'fr'));
}

/** The house's total for the month. */
export function totalBytes(usage: Usage[]): number {
  return usage.reduce((n, u) => n + u.bytes, 0);
}

/**
 * Wraps a sampling call so that it reaches the box at most once per gap (the
 * device list and the Conso tab both refresh, and the box keeps one admin
 * session). Resolves true when a sample was recorded; a failure resolves
 * false and does not hold the next attempt back.
 */
export function createSampler(sample: () => Promise<unknown>, gapMs: number, now: () => number = () => Date.now()) {
  let last = Number.NEGATIVE_INFINITY;
  return async (): Promise<boolean> => {
    const t = now();
    if (t - last < gapMs) return false;
    last = t;
    try {
      await sample();
      return true;
    } catch {
      last = Number.NEGATIVE_INFINITY;
      return false;
    }
  };
}

/**
 * How far back the month selector goes: the core keeps the daily figures 13
 * months, this one and the 12 before it (domain.UsageRetentionMonths).
 */
export const OLDEST_MONTHS_BACK = 12;

/** How many months the device sheet's curve shows. */
export const CURVE_MONTHS = 6;

/** The month of a date, "YYYY-MM", in the phone's time zone. */
export function periodOf(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`;
}

/** The month `delta` months after `period` (before, when negative). */
export function shiftPeriod(period: string, delta: number): string {
  const [y, m] = period.split('-').map(Number);
  return periodOf(new Date(y, m - 1 + delta, 1));
}

const MONTHS = ['janvier', 'février', 'mars', 'avril', 'mai', 'juin', 'juillet', 'août', 'septembre', 'octobre', 'novembre', 'décembre'];
const SHORT = ['janv.', 'févr.', 'mars', 'avr.', 'mai', 'juin', 'juil.', 'août', 'sept.', 'oct.', 'nov.', 'déc.'];

const monthIndex = (period: string) => Number(period.split('-')[1]) - 1;

/** "octobre 2026". */
export function monthLabel(period: string): string {
  return `${MONTHS[monthIndex(period)] ?? period} ${period.split('-')[0]}`;
}

/** "oct.", under a bar of the curve. */
export function shortMonth(period: string): string {
  return SHORT[monthIndex(period)] ?? period;
}

/** One month of a device's curve. share is its height, 0..1. */
export type CurveBar = { period: string; bytes: number; share: number };

/** The device's months as bars, relative to its busiest month. */
export function curveBars(history: Usage[]): CurveBar[] {
  const top = Math.max(0, ...history.map((u) => u.bytes));
  return history.map((u) => ({ period: u.period, bytes: u.bytes, share: top > 0 ? u.bytes / top : 0 }));
}
