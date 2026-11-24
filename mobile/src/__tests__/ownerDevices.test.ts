import { createOwnerDevices, type OwnerDevicesDeps } from '../features/ownerDevices';
import { HubError } from '../licensing/api';

// The owner's phones (#87, parent #69): the hub keeps the household's list;
// every phone fetches it with the household data and hands it to the core,
// which marks those devices Owner. The owner's phone, once it sees itself on
// the box (the core's Self), declares its MAC there.

const ME = 'fa:4b:47:00:00:86';

function deps() {
  let hubList: string[] = [];
  const d = {
    signedIn: jest.fn(() => true),
    hub: {
      list: jest.fn(async () => hubList),
      declare: jest.fn(async (mac: string, _key: string) => void (hubList = [mac])),
    },
    core: { set: jest.fn(async (_macs: string[], _tenantId?: string) => undefined) },
    isOwner: jest.fn(async (): Promise<{ owner: boolean; keyId: string }> => ({ owner: true, keyId: 'K1' })),
    now: jest.fn(() => 0),
    every: 60_000,
  };
  const typed: OwnerDevicesDeps = d;
  void typed;
  return { d, setHub: (l: string[]) => (hubList = l) };
}

const self = (mac: string) => ({ MAC: mac.toUpperCase(), Mark: 'Self' as const });

test('not signed in: nothing is asked nor handed', async () => {
  const { d } = deps();
  d.signedIn.mockReturnValue(false);
  const o = createOwnerDevices(d);
  await o.sync();
  await o.declare([self(ME)]);
  expect(d.hub.list).not.toHaveBeenCalled();
  expect(d.core.set).not.toHaveBeenCalled();
  expect(d.hub.declare).not.toHaveBeenCalled();
});

test("the hub's list is handed to the core, for the box or for the household's remote list", async () => {
  const { d, setHub } = deps();
  setHub(['AA:00:00:00:00:01']);
  const o = createOwnerDevices(d);
  await o.sync();
  expect(d.core.set).toHaveBeenLastCalledWith(['aa:00:00:00:00:01'], undefined);
  await o.sync({ tenantId: 'T1' });
  expect(d.core.set).toHaveBeenLastCalledWith(['aa:00:00:00:00:01'], 'T1');
});

test('the owner phone declares the MAC it has on the box, once', async () => {
  const { d } = deps();
  const o = createOwnerDevices(d);
  await o.sync();
  await o.declare([{ MAC: 'ac:00', Mark: undefined }, self(ME)]);
  expect(d.hub.declare).toHaveBeenCalledWith(ME, 'K1');
  // The next sync reads it back from the hub: nothing more to declare.
  await o.sync();
  await o.declare([self(ME)]);
  expect(d.hub.declare).toHaveBeenCalledTimes(1);
});

test("a member's phone (or an admin's) declares nothing", async () => {
  const { d } = deps();
  d.isOwner.mockResolvedValue({ owner: false, keyId: '' });
  const o = createOwnerDevices(d);
  await o.sync();
  await o.declare([self(ME)]);
  expect(d.hub.declare).not.toHaveBeenCalled();
  // …and is not asked again at every listing.
  await o.declare([self(ME)]);
  expect(d.isOwner).toHaveBeenCalledTimes(1);
});

test('a refused declaration (the key changed hands) is not retried at once', async () => {
  const { d } = deps();
  d.hub.declare.mockRejectedValue(new HubError(409, 'CONFLICT', 'not the owner key'));
  const o = createOwnerDevices(d);
  await o.sync();
  await expect(o.declare([self(ME)])).resolves.toBeUndefined();
  await o.declare([self(ME)]);
  expect(d.hub.declare).toHaveBeenCalledTimes(1);
});

test('without « this phone » on the list, nothing is declared', async () => {
  const { d } = deps();
  const o = createOwnerDevices(d);
  await o.sync();
  await o.declare([{ MAC: 'ac:00' }]);
  expect(d.isOwner).not.toHaveBeenCalled();
});
