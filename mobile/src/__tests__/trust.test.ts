// The closed Wi-Fi's rules in the app (#108, RFC-007 D1, D3, D5, D11, D12),
// with the owner's arbitration (#111): what the card says, the device's
// state and its pill, the choices offered, the places, and the first
// « Voici les appareils de la maison ».
import type { Device, WiFiLockPreview, WiFiLockStatus } from '../core/types';
import {
  houseDevices,
  lockView,
  placesLine,
  reserveCheck,
  trustChoices,
  trustOf,
  trustPill,
  visitorCandidates,
} from '../lib/trust';

const NOW = new Date('2026-11-28T12:00:00Z').getTime();
const DAY = 86_400_000;

const device = (over: Partial<Device> = {}): Device => ({
  ID: 'x',
  MAC: 'ac:bb:cc:00:00:22',
  IP: '192.168.1.10',
  Hostname: 'Laptop-Bureau',
  CustomName: '',
  Vendor: '',
  SSID: 'MonWiFi-2G',
  FirstSeen: new Date(NOW - 40 * DAY).toISOString(),
  LastSeen: new Date(NOW - 60_000).toISOString(),
  Connected: true,
  Blocked: false,
  NewlySeen: false,
  ...over,
});

const status = (over: Partial<WiFiLockStatus> = {}): WiFiLockStatus => ({
  state: 'open',
  aps: [],
  household_aps: [],
  visitor_aps: [],
  visitors_closed: false,
  window: { until: '', minutes_left: 0, can_extend: false },
  allowed: 0,
  places: 32,
  places_known: true,
  found_open: false,
  intended: false,
  since: '',
  cut: [],
  ...over,
});

const preview = (over: Partial<WiFiLockPreview> = {}): WiFiLockPreview => ({
  household_aps: ['DEV.WIFI.AP1'],
  visitor_aps: [],
  closed_aps: ['DEV.WIFI.AP1'],
  visitors_closed: false,
  devices: ['fa:4b:47:00:00:86', 'ac:bb:cc:00:00:22'],
  always: ['fa:4b:47:00:00:86'],
  relays: [],
  self: 'fa:4b:47:00:00:86',
  self_private: false,
  places: 32,
  places_known: true,
  full: null,
  closed: false,
  ...over,
});

describe('the card: always the box’s state, never the action (R3, R12)', () => {
  it('names each state by its effect (R2)', () => {
    expect(lockView(status())).toMatchObject({ state: 'open', title: 'Ouvert avec mot de passe', reserved: false });
    expect(lockView(status({ state: 'closed' }))).toMatchObject({ state: 'closed', title: 'Réservé à la maison', reserved: true });
    expect(lockView(status({ state: 'closed' }))?.line).toBe(
      'Le mot de passe ou un QR code partagé ne suffisent plus : seuls vos appareils entrent. Internet marche.',
    );
    expect(lockView(status({ state: 'busy' }))).toMatchObject({ state: 'busy', title: 'En cours…' });
  });

  it('counts the add window down in minutes (R12)', () => {
    const v = lockView(status({ state: 'window', window: { until: 'x', minutes_left: 3, can_extend: true } }));
    expect(v).toMatchObject({ state: 'window', title: 'Ajout d’appareil : encore 3 min', reserved: true });
  });

  it('says an add window nobody closed is over', () => {
    expect(lockView(status({ state: 'open', intended: true }))?.ended).toBe(true);
    // Reserved, then opened by hand: nothing to say.
    expect(lockView(status({ state: 'open', intended: false }))?.ended).toBe(false);
  });

  it('is nothing on a box that cannot (D11)', () => {
    expect(lockView(null)).toBeNull();
    expect(lockView(undefined)).toBeNull();
  });
});

describe('one state per device (D1)', () => {
  it('blocked wins, then authorised, else to check', () => {
    expect(trustOf(device({ Blocked: true, Approved: true }), false)).toBe('blocked');
    expect(trustOf(device(), true)).toBe('blocked'); // cut off the Wi-Fi
    expect(trustOf(device({ Approved: true }), false)).toBe('authorised');
    expect(trustOf(device(), false)).toBe('check');
  });
});

describe('the pill: colour, icon and word (R5, R7, R13)', () => {
  const ctx = { capable: true, reserved: false, cut: false, now: NOW };

  it('blocked, with what it means for it', () => {
    expect(trustPill(device({ Blocked: true }), ctx)).toEqual({
      trust: 'blocked',
      role: 'blocked',
      icon: 'block-helper',
      label: 'Bloqué',
      sub: 'Pas d’internet',
    });
    expect(trustPill(device({ Blocked: true }), { ...ctx, cut: true })?.sub).toBe('Ne peut plus se connecter');
  });

  it('authorised in green', () => {
    expect(trustPill(device({ Approved: true }), ctx)).toMatchObject({ role: 'connected', icon: 'check-decagram', label: 'Autorisé' });
  });

  it('open: orange only for a device without a name seen these 7 days, neutral otherwise', () => {
    expect(trustPill(device(), ctx)).toMatchObject({ role: 'attention', label: 'À vérifier' });
    expect(trustPill(device({ CustomName: 'TV salon' }), ctx)).toMatchObject({ role: 'neutral', label: 'À vérifier' });
    expect(trustPill(device({ Connected: false, LastSeen: new Date(NOW - 8 * DAY).toISOString() }), ctx)).toMatchObject({ role: 'neutral' });
  });

  it('reserved: every device to check is orange (it is refused)', () => {
    expect(trustPill(device({ CustomName: 'TV salon' }), { ...ctx, reserved: true })).toMatchObject({ role: 'attention' });
  });

  it('no emoji in a word (R13)', () => {
    for (const d of [device(), device({ Approved: true }), device({ Blocked: true })]) {
      expect(trustPill(d, ctx)?.label).toMatch(/^[\p{L} ’']+$/u);
    }
  });

  it('a box that cannot reserve its Wi-Fi never says « Autorisé » (D11)', () => {
    expect(trustPill(device({ Approved: true }), { ...ctx, capable: false })).toBeNull();
    expect(trustPill(device({ Blocked: true }), { ...ctx, capable: false })?.label).toBe('Bloqué');
  });
});

describe('the choices (D5, D8, R4)', () => {
  const base = { capable: true, reserved: false, current: 'check' as const, mark: undefined };
  const byTrust = (cs: ReturnType<typeof trustChoices>) => Object.fromEntries(cs.map((c) => [c.trust, c]));

  it('open: Autorisé, À vérifier, Bloqué, each with its sentence', () => {
    const cs = trustChoices(base);
    expect(cs.map((c) => [c.label, c.line])).toEqual([
      ['Autorisé', 'Peut toujours se connecter.'],
      ['À vérifier', 'Ni autorisé ni bloqué : il est signalé quand il se connecte.'],
      ['Bloqué', 'Il n’a plus internet.'],
    ]);
  });

  it('reserved: the middle choice says what it does (R4)', () => {
    const c = byTrust(trustChoices({ ...base, reserved: true, current: 'authorised' })).check;
    expect([c.label, c.line]).toEqual(['Retirer de la maison', 'Il sera déconnecté du Wi-Fi.']);
    // It refuses the device: Basic, like a block (D8).
    expect(c.feature).toBe('cut_wifi_access');
    // Open, it writes nothing on the box: free.
    expect(byTrust(trustChoices({ ...base, current: 'authorised' })).check.feature).toBeNull();
  });

  it('authorising and unblocking are free, blocking is Basic', () => {
    const cs = byTrust(trustChoices({ ...base, current: 'blocked' }));
    expect(cs.authorised.feature).toBeNull();
    expect(cs.check.feature).toBeNull();
    expect(byTrust(trustChoices(base)).blocked.feature).toBe('block');
  });

  it('this phone: « Bloqué » off with « C’est ce téléphone », and the middle one too when reserved', () => {
    const open = byTrust(trustChoices({ ...base, mark: 'Self', current: 'authorised' }));
    expect(open.blocked.disabled).toBe('C’est ce téléphone');
    expect(open.check.disabled).toBeNull();
    const closed = byTrust(trustChoices({ ...base, mark: 'Self', current: 'authorised', reserved: true }));
    expect(closed.check.disabled).toBe('C’est ce téléphone');
  });

  it('an owner’s phone is never blocked, but what blocked it stays undoable', () => {
    expect(byTrust(trustChoices({ ...base, mark: 'Owner' })).blocked.disabled).toBe('C’est un téléphone du propriétaire');
    expect(byTrust(trustChoices({ ...base, mark: 'Owner', current: 'blocked' })).check.disabled).toBeNull();
  });

  it('a box that cannot reserve its Wi-Fi still offers « Autorisé »: the alerts’ authorisation (#58)', () => {
    const cs = trustChoices({ ...base, capable: false });
    expect(cs.map((c) => c.trust)).toEqual(['authorised', 'check', 'blocked']);
    expect(cs[0].line).toBe('Il n’est plus signalé quand il rejoint le Wi-Fi.');
  });
});

describe('the places (D3)', () => {
  it('counts against what the box holds, never 32 written in', () => {
    expect(placesLine(18, 32, true)).toEqual({ text: '18 / 32 places', warn: false, full: false });
    expect(placesLine(26, 32, true)).toMatchObject({ warn: true, full: false });
    expect(placesLine(21, 21, true)).toMatchObject({ warn: true, full: true });
    expect(placesLine(5, 64, true).text).toBe('5 / 64 places');
  });

  it('without a known limit, the count alone', () => {
    expect(placesLine(7, 0, false)).toEqual({ text: '7 autorisés', warn: false, full: false });
  });
});

describe('« Réserver mon Wi-Fi à la maison » is off until this phone is in (D12, D9)', () => {
  it('off while the core does not find this phone on the box', () => {
    expect(reserveCheck(preview({ self: '' }), { viaRelay: false, hasRelay: false })).toMatchObject({
      ok: false,
      why: 'Autorisez d’abord ce téléphone : Kergui ne le trouve pas sur la box.',
    });
    expect(reserveCheck(preview(), { viaRelay: false, hasRelay: false })).toMatchObject({ ok: true, warnings: [] });
  });

  it('through the relay, the relay must be in; this phone left out is a warning', () => {
    expect(reserveCheck(preview({ self: '', relays: [] }), { viaRelay: true, hasRelay: true }).ok).toBe(false);
    const r = reserveCheck(preview({ self: '', relays: ['02:aa:00:00:00:01'] }), { viaRelay: true, hasRelay: true });
    expect(r.ok).toBe(true);
    expect(r.warnings).toContain('Ce téléphone n’est pas autorisé : il sera refusé en rentrant à la maison.');
  });

  it('a household relay whose address Kergui does not know is said before anything is written', () => {
    const r = reserveCheck(preview({ relays: [] }), { viaRelay: false, hasRelay: true });
    expect(r.ok).toBe(true);
    expect(r.warnings[0]).toMatch(/relais ne pourra plus s’y connecter/);
  });

  it('a full list says the numbers, and writes nothing', () => {
    const r = reserveCheck(preview({ full: { kind: 'wifi', limit: 32, used: 33 } }), { viaRelay: false, hasRelay: false });
    expect(r).toMatchObject({ ok: false, why: 'La box accepte 32 appareils au plus. Retirez un appareil que vous n’utilisez plus.' });
  });

  it('the private address advice, in plain words (R10)', () => {
    expect(reserveCheck(preview({ self_private: true }), { viaRelay: false, hasRelay: false }).privateAddress).toBe(true);
  });
});

describe('« Voici les appareils de la maison » (R5, R8)', () => {
  const self = device({ MAC: 'fa:4b:47:00:00:86', Mark: 'Self', Hostname: 'Mon-Telephone' });
  const owner = device({ MAC: 'fa:4b:47:00:00:87', Mark: 'Owner', Hostname: 'Tel-Proprio' });
  const tv = device({ MAC: 'ac:00:00:00:00:01', CustomName: 'TV salon' });
  const known = device({ MAC: 'ac:00:00:00:00:02', Hostname: 'Imprimante', Approved: true });
  const stranger = device({ MAC: 'ac:00:00:00:00:03', Hostname: '' });
  const old = device({ MAC: 'ac:00:00:00:00:04', CustomName: 'Vieux PC', Connected: false, LastSeen: new Date(NOW - 31 * DAY).toISOString() });
  const blocked = device({ MAC: 'ac:00:00:00:00:05', CustomName: 'Bloqué', Blocked: true });
  const relay = device({ MAC: '02:aa:00:00:00:01', Hostname: 'Relais' });
  const all = [self, owner, tv, known, stranger, old, blocked, relay];

  it('two groups: named or authorised checked, unnamed seen lately unchecked', () => {
    const h = houseDevices(all, { relays: [relay.MAC], places: 32, placesKnown: true, now: NOW });
    expect(h.always.map((d) => d.MAC)).toEqual([self.MAC, owner.MAC, relay.MAC]);
    expect(h.named.map((d) => d.MAC)).toEqual([tv.MAC, known.MAC]);
    expect(h.recent.map((d) => d.MAC)).toEqual([stranger.MAC]);
    expect([...h.checked].sort()).toEqual([known.MAC, tv.MAC].sort());
  });

  it('leaves out the blocked and the devices not seen for 30 days', () => {
    const h = houseDevices(all, { relays: [], places: 32, placesKnown: true, now: NOW });
    const listed = [...h.always, ...h.named, ...h.recent].map((d) => d.MAC);
    expect(listed).not.toContain(old.MAC);
    expect(listed).not.toContain(blocked.MAC);
  });

  it('caps at the places, the oldest seen unchecked first', () => {
    const a = device({ MAC: 'ac:00:00:00:00:0a', CustomName: 'A', Connected: false, LastSeen: new Date(NOW - 5 * DAY).toISOString() });
    const b = device({ MAC: 'ac:00:00:00:00:0b', CustomName: 'B', Connected: false, LastSeen: new Date(NOW - 1 * DAY).toISOString() });
    const h = houseDevices([self, a, b], { relays: [], places: 2, placesKnown: true, now: NOW });
    // This phone takes one place: one left, for the one seen last.
    expect([...h.checked]).toEqual([b.MAC]);
    expect(h.capped).toBe(true);
  });
});

describe('the visitors’ question (D2, R9)', () => {
  const aps = [
    { id: 'DEV.WIFI.AP1', band: '2.4GHz', name: 'MonWiFi', mode: 'Disabled', visitors: false },
    { id: 'DEV.WIFI.AP5', band: '5GHz', name: 'MonWiFi', mode: 'Disabled', visitors: false },
    { id: 'DEV.WIFI.AP3', band: '2.4GHz', name: 'Invites', mode: 'Disabled', visitors: false },
  ];

  it('asks for each network other than this phone’s and its twin, with its band and devices on it', () => {
    const devices = [device({ SSID: 'MonWiFi', Mark: 'Self' }), device({ SSID: 'Invites' }), device({ SSID: 'Invites', Connected: false })];
    expect(visitorCandidates(aps, devices)).toEqual([{ name: 'Invites', ids: ['DEV.WIFI.AP3'], bands: '2,4 GHz', connected: 1 }]);
  });
});
