/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The household's names are the same at home and through the relay (#95).
// The owner's phone sends its names to the relay, signed; it takes the ones
// given through the relay into its core; and until the relay has them, the
// list through the relay is merged with the phone's own names by MAC, the
// later edit winning — nothing ever looks « reset ». A member sees them too.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
  Stack: { Screen: () => null },
  useLocalSearchParams: () => ({}),
}));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../alerts/alerts', () => ({ checkNewDevices: async () => 0, enforceAutoReject: async () => 0 }));
jest.mock('../features/relayPolicy', () => ({ syncRelayPolicy: async () => undefined }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));
jest.mock('../licensing/household', () => ({
  withHousehold: (run: (tok: string, id: string) => Promise<unknown>) => run('tok', 'T1'),
}));
const mockHub = {
  trustBundle: jest.fn(),
  routerDevices: jest.fn(),
  relayStatus: jest.fn(),
  deviceCommand: jest.fn(),
  routerCommand: jest.fn(),
  command: jest.fn(),
};
jest.mock('../licensing/hub', () => ({
  get hub() {
    return mockHub;
  },
}));

import KerguiCore from '../../modules/kergui-core';
import Devices from '../app/(tabs)/devices';
import type { Device, DeviceLabel } from '../core/types';
import { createLabelSync, mergeLabels, type LabelSyncDeps, type RelayList } from '../features/labels';
import type { RemoteHousehold } from '../features/remote';
import { useAccount } from '../licensing/account';
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';

const MAC = 'ac:bb:cc:00:00:22';
const T0 = Date.parse('2026-11-25T09:00:00Z');
const at = (min: number) => new Date(T0 + min * 60_000).toISOString();

function device(over: Partial<Device> = {}): Device {
  return {
    ID: '',
    MAC,
    IP: '192.168.1.10',
    Hostname: 'Laptop-Relais',
    CustomName: '',
    Vendor: '',
    SSID: 'MonWiFi-2G',
    FirstSeen: '',
    LastSeen: '',
    Connected: true,
    Blocked: false,
    NewlySeen: false,
    ...over,
  };
}

const OWNER: RemoteHousehold = { tenantId: 'T1', bundle: null, relays: [{ agentId: 'A1', kind: 'phone' }] };
const MEMBER: RemoteHousehold = { ...OWNER, member: { control: true, ownerOnly: false } };

describe('the merge by MAC', () => {
  test("the relay's list without the names shows the phone's", () => {
    const [d] = mergeLabels([device()], [{ mac: MAC.toUpperCase(), name: 'Laptop de Moussa', at: at(0) }]);
    expect(d.CustomName).toBe('Laptop de Moussa');
  });

  test('the later edit wins, either way', () => {
    const relayNewer = device({ CustomName: 'Tablette de Awa', LabelAt: at(5) });
    expect(mergeLabels([relayNewer], [{ mac: MAC, name: 'Vieux nom', at: at(1) }])[0].CustomName).toBe('Tablette de Awa');
    const phoneNewer = device({ CustomName: 'Vieux nom', LabelAt: at(1) });
    expect(mergeLabels([phoneNewer], [{ mac: MAC, name: 'Tablette', at: at(5) }])[0].CustomName).toBe('Tablette');
  });
});

/** A labelSync on fakes: this phone's core, the relay, a clock. */
function world(opts: { mine?: DeviceLabel[]; relay?: Partial<RelayList>; coreless?: boolean } = {}) {
  let mine = [...(opts.mine ?? [])];
  const list: RelayList = { agentId: 'A1', asOf: at(0), devices: [device()], ...opts.relay };
  const sent: DeviceLabel[][] = [];
  let clock = T0 + 10 * 60_000;
  const apply = (labels: DeviceLabel[]) => {
    const changed: string[] = [];
    for (const l of labels) {
      const cur = mine.find((m) => m.mac === l.mac);
      if (cur && Date.parse(cur.at) >= Date.parse(l.at)) continue;
      mine = [...mine.filter((m) => m.mac !== l.mac), { mac: l.mac, name: l.name ?? cur?.name, notes: l.notes ?? cur?.notes, at: l.at }];
      changed.push(l.mac);
    }
    return changed;
  };
  const deps: LabelSyncDeps = {
    core: {
      labels: async () => {
        if (opts.coreless) throw new Error('not configured');
        return mine;
      },
      apply: async (labels) => {
        if (opts.coreless) throw new Error('not configured');
        return apply(labels);
      },
      rename: async (mac, name) => {
        if (opts.coreless) throw new Error('not configured');
        apply([{ mac, name, at: new Date(clock).toISOString() }]);
      },
    },
    relay: {
      list: async () => list,
      send: async (_h, labels) => {
        sent.push(labels);
        return labels.map((l) => l.mac);
      },
    },
    store: { load: async () => null, save: async () => undefined },
    now: () => clock,
    every: 0,
    retryAfter: 0,
  };
  return {
    sync: createLabelSync(deps),
    list,
    sent,
    mine: () => mine,
    tick: (ms: number) => (clock += ms),
    deps,
  };
}

const flush = () => new Promise((ok) => setTimeout(ok, 0));

describe("the owner's names reach the relay", () => {
  test('a new relay is sent every name the phone has, once', async () => {
    const w = world({ mine: [{ mac: MAC, name: 'Laptop de Moussa', notes: '', at: at(0) }] });
    const shown = await w.sync.view(OWNER, w.list);
    expect(shown[0].CustomName).toBe('Laptop de Moussa');
    await flush();
    expect(w.sent).toEqual([[{ mac: MAC, name: 'Laptop de Moussa', notes: '', at: at(0) }]]);

    // Nothing new, the relay has not read the box since: nothing more.
    await w.sync.tick(OWNER);
    expect(w.sent).toHaveLength(1);

    // Another relay took the lease: it gets them too.
    w.list.agentId = 'A2';
    await w.sync.tick(OWNER);
    expect(w.sent).toHaveLength(2);
  });

  test('a rename at home goes to the relay at once', async () => {
    const w = world();
    await w.sync.rename(OWNER, MAC, 'Imprimante');
    expect(w.mine()[0].name).toBe('Imprimante');
    expect(w.sent.at(-1)?.[0]).toMatchObject({ mac: MAC, name: 'Imprimante' });
  });

  test('a relay whose list is still behind after it read the box is sent them again', async () => {
    const w = world({ mine: [{ mac: MAC, name: 'Laptop de Moussa', at: at(0) }] });
    await w.sync.tick(OWNER);
    expect(w.sent).toHaveLength(1);
    // The relay read the box after the send and still lists no name.
    w.tick(60_000);
    w.list.asOf = new Date(T0 + 30 * 60_000).toISOString();
    await w.sync.tick(OWNER);
    expect(w.sent).toHaveLength(2);
  });

  test('a send that fails leaves the name on screen and is tried again', async () => {
    const w = world({ mine: [{ mac: MAC, name: 'Laptop de Moussa', at: at(0) }] });
    w.deps.relay.send = async () => {
      throw new Error('hub too old');
    };
    const shown = await w.sync.view(OWNER, w.list);
    expect(shown[0].CustomName).toBe('Laptop de Moussa');
    const sent: DeviceLabel[][] = [];
    w.deps.relay.send = async (_h, labels) => {
      sent.push(labels);
      return [];
    };
    await flush();
    await w.sync.tick(OWNER);
    expect(sent).toHaveLength(1);
  });
});

describe('a rename through the relay', () => {
  test("lands in the owner's core when it reads the relay's list", async () => {
    const w = world({
      mine: [{ mac: MAC, name: 'Vieux nom', at: at(0) }],
      relay: { devices: [device({ CustomName: 'Tablette de Awa', LabelAt: at(3) })] },
    });
    const shown = await w.sync.view(OWNER, w.list);
    expect(shown[0].CustomName).toBe('Tablette de Awa');
    expect(w.mine()[0]).toMatchObject({ name: 'Tablette de Awa', at: at(3) });
  });

  test("a member's rename is sent through the relay and shown until the relay lists it", async () => {
    const w = world({ coreless: true });
    await w.sync.rename(MEMBER, MAC, 'Console');
    expect(w.sent[0][0]).toMatchObject({ mac: MAC, name: 'Console' });
    const shown = await w.sync.view(MEMBER, w.list);
    expect(shown[0].CustomName).toBe('Console');
  });

  test("a member is told when the rename could not reach the relay", async () => {
    const w = world({ coreless: true });
    w.deps.relay.send = async () => {
      throw new Error('relay_offline');
    };
    await expect(w.sync.rename(MEMBER, MAC, 'Console')).rejects.toThrow('relay_offline');
  });
});

// The Devices tab itself, away from home, as the owner and as a member.

const core = KerguiCore as unknown as Record<string, jest.Mock>;
const BOX = 'https://192.168.1.1';
const BUNDLE = { payload: 'UA==', sig: 'Uw==', version: 3 };
const OWNER_NAME = 'Laptop de Moussa';

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) {
    if (typeof c === 'string') out.push(c);
    else out.push(...texts(c));
  }
  return out;
}

const iconAsText = ({ name }: { name: string }) => <Text>{`icon:${name}`}</Text>;
let root: Root | null = null;
let qc: QueryClient | null = null;

async function settle(until: () => boolean = () => false) {
  for (let i = 0; i < 100 && !until(); i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 10))));
}

async function render(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false, gcTime: 0 } } });
  qc = client;
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={client}>
        <PaperProvider settings={{ icon: iconAsText }}>{ui}</PaperProvider>
      </QueryClientProvider>,
    );
  });
  return root;
}

const text = (r: Root) => texts(r.container).join('\n');

describe('the Devices tab through the relay', () => {
  let relayDevices: Device[];

  beforeEach(() => {
    for (const m of Object.values(mockHub)) m.mockReset();
    for (const k of ['request', 'certificateFingerprint', 'remoteRelays', 'openDeviceSnapshot', 'signRemoteRequest', 'verifyCommandResult']) core[k].mockReset();
    // Away: the box does not answer.
    core.certificateFingerprint.mockRejectedValue(new Error('dial tcp 192.168.1.1:443: i/o timeout'));
    useSession.setState({ session: { mode: 'router', routerUrl: BOX, pin: 'ab:cd' } });
    useAccount.setState({ account: { accountId: 'acc', phone: '+221770000001', sessionToken: 'kgs_x', expiresAt: '' } });
    useEntitlement.setState({ boxId: 'ZTEG00000001' });
    mockHub.trustBundle.mockResolvedValue(BUNDLE);
    const asOf = new Date().toISOString();
    mockHub.routerDevices.mockResolvedValue({ snapshot: { payload: 'cA==', sig: 'cw==' }, agentId: 'A1', asOf });
    mockHub.relayStatus.mockResolvedValue({ state: 'active', agentId: 'A1', lastSeenAt: asOf, ageSeconds: 5 });
    relayDevices = [device()];
    core.openDeviceSnapshot.mockImplementation(async () => JSON.stringify({ agentId: 'A1', asOf, devices: relayDevices }));
  });

  afterEach(async () => {
    await act(async () => root?.unmount());
    qc?.clear();
    root = qc = null;
  });

  test("the owner away sees their own names on the relay's list, and the relay is sent them", async () => {
    core.remoteRelays.mockResolvedValue(JSON.stringify({ owner: true, relays: [{ agentId: 'A1', kind: 'phone' }] }));
    core.request.mockImplementation(async (method: string, path: string) => {
      if (method === 'GET' && path === '/api/labels') {
        return { status: 200, body: JSON.stringify({ labels: [{ mac: MAC, name: OWNER_NAME, notes: '', at: at(0) }] }) };
      }
      if (method === 'POST' && path === '/api/labels') return { status: 200, body: '{"changed":[]}' };
      return { status: 502, body: '{"error":"router unreachable"}' };
    });
    core.signRemoteRequest.mockResolvedValue(
      JSON.stringify({ payload: 'Q01E', sig: 'U0lH', cmdId: 'c1', method: 'POST', path: '/api/labels', expiresAt: new Date(Date.now() + 60_000).toISOString() }),
    );
    mockHub.routerCommand.mockResolvedValue({ commandId: 'c1', status: 'done', result: { payload: 'cg==', sig: 'cw==' } });
    core.verifyCommandResult.mockResolvedValue(JSON.stringify({ agentId: 'A1', status: 200, body: { changed: [MAC] }, executedAt: new Date().toISOString() }));

    const r = await render(<Devices />);
    await settle(() => text(r).includes(OWNER_NAME) && core.signRemoteRequest.mock.calls.length > 0);
    expect(text(r)).toContain(OWNER_NAME);
    expect(text(r)).not.toContain('Laptop-Relais');

    const call = core.signRemoteRequest.mock.calls.find((c) => c[3] === '/api/labels');
    expect(call).toBeDefined();
    expect(call![2]).toBe('POST');
    expect(JSON.parse(call![4] as string)).toEqual({ labels: [{ mac: MAC, name: OWNER_NAME, notes: '', at: at(0) }] });
  });

  test("a member sees the owner's names the relay keeps", async () => {
    core.remoteRelays.mockResolvedValue(
      JSON.stringify({ owner: false, relays: [{ agentId: 'A1', kind: 'phone' }], member: { state: 'active', control: true }, ownerOnly: false }),
    );
    // A member's phone has no box: its core keeps no names of its own.
    core.request.mockRejectedValue(new Error('mobilecore: not configured'));
    relayDevices = [device({ CustomName: OWNER_NAME, LabelAt: at(0) })];

    const r = await render(<Devices />);
    await settle(() => text(r).includes(OWNER_NAME));
    expect(text(r)).toContain(OWNER_NAME);
    // A member never sends the household's names on its own.
    expect(core.signRemoteRequest.mock.calls.some((c) => c[3] === '/api/labels')).toBe(false);
  });
});
