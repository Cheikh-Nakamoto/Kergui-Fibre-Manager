// The automatic backup to the hub (issue #89, parent #70): the core's export
// goes to the hub after a change (debounced) and at most once a day for the
// consumption; a phone that signs in, or has no backup of its box, gets the
// hub's back when it is newer.
import { HubError } from '../licensing/api';
import { BackupUnavailable, createHubBackup, type HubBackupDeps } from '../features/hubBackup';

const DAY = 24 * 3600_000;

function setup(over: Partial<HubBackupDeps> = {}) {
  let clock = Date.parse('2026-11-24T10:00:00Z');
  let stored: string | null = null;
  const hubCopy = { at: '' as string, archive: '' };
  const core = {
    export: jest.fn(async () => ({ archive: 'UEsDBA-phone', name: 'kergui-sauvegarde-2026-11-24.zip', boxId: '192-168-1-1' })),
    import: jest.fn(async (_: string) => ({ devices: 3, named: 2, usage: 1 })),
  };
  const hub = {
    get: jest.fn(async (_box: string) =>
      hubCopy.at ? { boxId: '192-168-1-1', updatedAt: hubCopy.at, archive: hubCopy.archive, name: 'kergui-192-168-1-1.zip' } : null,
    ),
    put: jest.fn(async (_box: string, archive: string) => {
      hubCopy.at = new Date(clock).toISOString();
      hubCopy.archive = archive;
      return { boxId: '192-168-1-1', updatedAt: hubCopy.at, merged: false };
    }),
  };
  const deps: HubBackupDeps = {
    available: () => true,
    core,
    hub,
    store: { load: async () => stored, save: async (v) => void (stored = v) },
    now: () => clock,
    ...over,
  };
  return {
    backup: createHubBackup(deps),
    core,
    hub,
    hubCopy,
    advance: async (ms: number) => {
      clock += ms;
      await jest.advanceTimersByTimeAsync(ms);
    },
    stored: () => stored,
  };
}

beforeEach(() => jest.useFakeTimers());
afterEach(() => jest.useRealTimers());

test('sign-in on an empty phone fetches the box backup and imports it', async () => {
  const s = setup();
  s.hubCopy.at = '2026-11-20T08:00:00Z';
  s.hubCopy.archive = 'UEsDBA-hub';
  s.backup.signedIn();
  await s.backup.tick();
  expect(s.hub.get).toHaveBeenCalledWith('192-168-1-1');
  expect(s.core.import).toHaveBeenCalledWith('UEsDBA-hub');
  expect((await s.backup.status()).lastAt).toBe('2026-11-20T08:00:00Z');
  // In step with the hub: no send just after the import, nor a second import.
  await s.backup.tick();
  expect(s.core.import).toHaveBeenCalledTimes(1);
  expect(s.hub.put).not.toHaveBeenCalled();
});

test('a phone whose backup is as recent as the hub’s keeps its data', async () => {
  const s = setup();
  await s.backup.sendNow(); // this phone's backup is the hub's
  s.backup.signedIn();
  await s.backup.tick();
  expect(s.hub.get).toHaveBeenCalled();
  expect(s.core.import).not.toHaveBeenCalled();
});

test('a newer backup on the hub (another phone, the PC) is imported at the next sign-in', async () => {
  const s = setup();
  await s.backup.sendNow();
  await s.advance(3600_000);
  s.hubCopy.at = new Date(Date.parse(s.hubCopy.at) + 3600_000).toISOString();
  s.hubCopy.archive = 'UEsDBA-pc';
  s.backup.signedIn();
  await s.backup.tick();
  expect(s.core.import).toHaveBeenCalledWith('UEsDBA-pc');
});

test('nothing on the hub: nothing imported, the phone sends its own', async () => {
  const s = setup();
  s.backup.signedIn();
  await s.backup.tick();
  expect(s.core.import).not.toHaveBeenCalled();
  expect(s.hub.put).toHaveBeenCalledWith('192-168-1-1', 'UEsDBA-phone');
});

test('« Sauvegarder maintenant » sends the core export to the hub and records when', async () => {
  const s = setup();
  const out = await s.backup.sendNow();
  expect(s.core.export).toHaveBeenCalled();
  expect(s.hub.put).toHaveBeenCalledWith('192-168-1-1', 'UEsDBA-phone');
  expect(out.updatedAt).toBe('2026-11-24T10:00:00.000Z');
  expect((await s.backup.status()).lastAt).toBe('2026-11-24T10:00:00.000Z');
  // Kept across launches.
  expect(JSON.parse(s.stored()!).boxes['192-168-1-1'].at).toBe('2026-11-24T10:00:00.000Z');
});

test('« Sauvegarder maintenant » refuses without a hub (the demo, no account)', async () => {
  const s = setup({ available: () => false });
  await expect(s.backup.sendNow()).rejects.toBeInstanceOf(BackupUnavailable);
  expect(s.core.export).not.toHaveBeenCalled();
  expect(s.hub.put).not.toHaveBeenCalled();
});

test('changes are sent once, 30 s after the last one', async () => {
  const s = setup();
  s.backup.changed();
  await s.advance(20_000);
  s.backup.changed();
  await s.advance(20_000);
  s.backup.changed();
  await s.advance(29_000);
  expect(s.hub.put).not.toHaveBeenCalled();
  await s.advance(1_000);
  expect(s.hub.put).toHaveBeenCalledTimes(1);
});

test('the consumption goes at most once a day', async () => {
  const s = setup();
  await s.backup.tick();
  expect(s.hub.put).toHaveBeenCalledTimes(1);
  await s.advance(DAY - 10 * 60_000);
  await s.backup.tick();
  expect(s.hub.put).toHaveBeenCalledTimes(1);
  await s.advance(10 * 60_000);
  await s.backup.tick();
  expect(s.hub.put).toHaveBeenCalledTimes(2);
});

test('a member the hub does not let write is left alone, without noise', async () => {
  const s = setup();
  s.hub.put.mockRejectedValue(new HubError(403, 'FORBIDDEN', 'owner or admin'));
  await s.backup.tick();
  s.backup.changed();
  await s.advance(60_000);
  await s.backup.tick();
  expect(s.hub.put).toHaveBeenCalledTimes(1);
  // « Sauvegarder maintenant » still says why.
  await expect(s.backup.sendNow()).rejects.toMatchObject({ status: 403 });
});

test('an unsent change is sent at the next launch and the hub’s copy is not imported over it', async () => {
  const s = setup();
  s.hub.put.mockRejectedValueOnce(new HubError(0, 'NETWORK', 'offline'));
  s.backup.changed();
  await s.advance(30_000);
  // Next launch, after another phone backed up.
  s.hubCopy.at = '2026-11-25T00:00:00Z';
  s.hubCopy.archive = 'UEsDBA-other';
  const again = createHubBackup({
    available: () => true,
    core: s.core,
    hub: s.hub,
    store: { load: async () => s.stored(), save: async () => undefined },
    now: () => Date.parse('2026-11-24T11:00:00Z'),
  });
  again.signedIn();
  await again.tick();
  expect(s.core.import).not.toHaveBeenCalled();
  expect(s.hub.put).toHaveBeenCalledTimes(2);
});

test('the hub’s backup is downloaded as the same zip', async () => {
  const s = setup();
  s.hubCopy.at = '2026-11-20T08:00:00Z';
  s.hubCopy.archive = 'UEsDBA-hub';
  expect(await s.backup.download()).toEqual({ archive: 'UEsDBA-hub', name: 'kergui-192-168-1-1.zip' });
  s.hubCopy.at = '';
  expect(await s.backup.download()).toBeNull();
});
