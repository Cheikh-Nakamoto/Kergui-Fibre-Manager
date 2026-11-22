import KerguiCore from '../../modules/kergui-core';
import { setupMACFilter } from '../session/filterSetup';

// The native module is mocked in jest.setup.ts.
const request = KerguiCore.request as jest.Mock;
const note = KerguiCore.note as jest.Mock;

beforeEach(() => {
  request.mockReset();
  note.mockReset();
});

test('asks the core to switch the MAC filter on after sign-in', async () => {
  request.mockResolvedValue({ status: 200, body: '{"result":"enabled"}' });
  await expect(setupMACFilter()).resolves.toBe('enabled');
  expect(request).toHaveBeenCalledWith('POST', '/api/filter/setup', '');
});

test('an unsupported box is no error for the user', async () => {
  request.mockResolvedValue({ status: 200, body: '{"result":"unsupported"}' });
  await expect(setupMACFilter()).resolves.toBe('unsupported');
});

test('a failure stays in the journal, never on screen', async () => {
  request.mockResolvedValue({ status: 502, body: '{"error":"router unreachable"}' });
  await expect(setupMACFilter()).resolves.toBeNull();
  expect(note).toHaveBeenCalledWith('warn', expect.stringContaining('filtrage MAC'));
});
