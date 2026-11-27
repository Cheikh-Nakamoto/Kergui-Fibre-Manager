import { createApprovals, type ApprovalsDeps } from '../features/approvals';
import { HubError } from '../licensing/api';

// Approvals (#58): kept by the core for the box (what the Android watch
// reads), and by the hub for the household when the phone is signed in to
// one — the hub's list then wins, after this phone pushed what it approved
// on its own (the notification's « Autoriser », names given before #58).

function deps() {
  let local = { macs: [] as string[], unsynced: [] as string[] };
  const hubSet = new Set<string>();
  const d = {
    core: {
      list: jest.fn(async () => local),
      replace: jest.fn(async (macs: string[]) => void (local = { macs, unsynced: [] })),
      approve: jest.fn(async (mac: string) => void (local = { macs: [...local.macs, mac], unsynced: [...local.unsynced, mac] })),
      withdraw: jest.fn(async (mac: string) => {
        local = { macs: local.macs.filter((m) => m !== mac), unsynced: local.unsynced.filter((m) => m !== mac) };
      }),
    },
    hub: {
      signedIn: jest.fn(() => true),
      approved: jest.fn(async () => [...hubSet]),
      approve: jest.fn(async (mac: string) => void hubSet.add(mac)),
      approveMany: jest.fn(async (macs: string[]) => macs.forEach((m) => hubSet.add(m))),
      withdraw: jest.fn(async (mac: string) => void hubSet.delete(mac)),
    },
  };
  const typed: ApprovalsDeps = d;
  void typed;
  return { d, hubSet, local: () => local, setLocal: (l: typeof local) => (local = l) };
}

test('without a hub, the core is the list', async () => {
  const { d, setLocal } = deps();
  d.hub.signedIn.mockReturnValue(false);
  setLocal({ macs: ['aa:00'], unsynced: ['aa:00'] });
  const a = createApprovals(d);
  expect([...(await a.sync())]).toEqual(['aa:00']);
  expect(d.hub.approved).not.toHaveBeenCalled();
  await a.approve('AA:01');
  expect(d.core.approve).toHaveBeenCalledWith('aa:01');
  expect(d.hub.approve).not.toHaveBeenCalled();
});

test('signed in, what this phone approved alone is pushed, then the hub list wins', async () => {
  const { d, hubSet, local, setLocal } = deps();
  hubSet.add('bb:00');
  // A name given before #58 (the core's upgrade) and a notification's « Autoriser ».
  setLocal({ macs: ['aa:00', 'aa:01'], unsynced: ['aa:00', 'aa:01'] });
  const set = await createApprovals(d).sync();
  expect(d.hub.approveMany).toHaveBeenCalledWith(['aa:00', 'aa:01']);
  expect([...set].sort()).toEqual(['aa:00', 'aa:01', 'bb:00']);
  expect(local()).toEqual({ macs: expect.arrayContaining(['aa:00', 'aa:01', 'bb:00']), unsynced: [] });
});

test('a withdrawal on another phone reaches this one', async () => {
  const { d, local, setLocal } = deps();
  setLocal({ macs: ['aa:00'], unsynced: [] }); // synced earlier, withdrawn on the hub since
  expect([...(await createApprovals(d).sync())]).toEqual([]);
  expect(local().macs).toEqual([]);
});

test('approve and withdraw go to the hub and the core', async () => {
  const { d, hubSet, local } = deps();
  const a = createApprovals(d);
  await a.approve('AA:BB:CC:00:00:58');
  expect(hubSet.has('aa:bb:cc:00:00:58')).toBe(true);
  expect(local().macs).toEqual(['aa:bb:cc:00:00:58']);
  await a.withdraw('aa:bb:cc:00:00:58');
  expect(hubSet.size).toBe(0);
  expect(local().macs).toEqual([]);
});

test('a hub that cannot be reached leaves the core list as it is', async () => {
  const { d, setLocal } = deps();
  d.hub.approved.mockRejectedValue(new HubError(0, 'NETWORK', 'offline'));
  d.hub.approveMany.mockRejectedValue(new HubError(0, 'NETWORK', 'offline'));
  setLocal({ macs: ['aa:00'], unsynced: ['aa:00'] });
  expect([...(await createApprovals(d).sync())]).toEqual(['aa:00']);
  expect(d.core.replace).not.toHaveBeenCalled();
});

test('a household with no inventory yet keeps what this phone approved', async () => {
  const { d, setLocal } = deps();
  d.hub.approveMany.mockRejectedValue(new HubError(404, 'NOT_FOUND', 'no inventory'));
  d.hub.approved.mockResolvedValue([]);
  setLocal({ macs: ['aa:00'], unsynced: ['aa:00'] });
  expect([...(await createApprovals(d).sync())]).toEqual(['aa:00']);
});

test("through the relay, the hub alone: the phone's core is not the box's", async () => {
  const { d, hubSet } = deps();
  hubSet.add('bb:00');
  const a = createApprovals(d);
  expect([...(await a.sync({ remote: true }))]).toEqual(['bb:00']);
  await a.approve('AA:01', { remote: true });
  await a.withdraw('bb:00', { remote: true });
  expect([...hubSet]).toEqual(['aa:01']);
  expect(d.core.list).not.toHaveBeenCalled();
  expect(d.core.approve).not.toHaveBeenCalled();
  expect(d.core.withdraw).not.toHaveBeenCalled();
});
