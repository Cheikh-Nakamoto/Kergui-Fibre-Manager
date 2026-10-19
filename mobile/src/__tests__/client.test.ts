import KerguiCore from '../../modules/kergui-core';
import { ApiError, api, call, core } from '../core/client';
import { errorMessage } from '../i18n';

/** The ApiError a call rejects with (fails the test if it resolves). */
async function rejection(p: Promise<unknown>): Promise<ApiError> {
  try {
    await p;
  } catch (e) {
    return e as ApiError;
  }
  throw new Error('expected the call to fail');
}

// The native module is mocked in jest.setup.ts.
const request = KerguiCore.request as jest.Mock;

beforeEach(() => request.mockReset());

test('passes method, path and JSON body to the core', async () => {
  request.mockResolvedValue({ status: 200, body: '{"enabled":true}' });
  await expect(api.setFilter(true)).resolves.toEqual({ enabled: true });
  expect(request).toHaveBeenCalledWith('POST', '/api/filter/toggle', '{"enabled":true}');
});

test('encodes the MAC in the path and sends no body for block', async () => {
  request.mockResolvedValue({ status: 200, body: '{}' });
  await api.block('ac:bb:cc:00:00:22');
  expect(request).toHaveBeenCalledWith('POST', '/api/devices/ac%3Abb%3Acc%3A00%3A00%3A22/block', '');
});

test('turns a 4xx/5xx into an ApiError carrying the server message', async () => {
  request.mockResolvedValue({ status: 501, body: '{"error":"not implemented: block on zte_f680"}' });
  const err = await rejection(call('POST', '/api/devices/x/block'));
  expect(err).toBeInstanceOf(ApiError);
  expect(err.status).toBe(501);
  expect(err.notSupported).toBe(true);
  expect(err.message).toContain('zte_f680');
  expect(errorMessage(err)).toBe('Pas encore pris en charge sur votre modèle de box.');
});

test('recognises a certificate pin mismatch', async () => {
  request.mockResolvedValue({
    status: 502,
    body: '{"error":"router unreachable: router certificate does not match the pinned fingerprint: got ab"}',
  });
  const err = await rejection(api.devices());
  expect(err.pinMismatch).toBe(true);
  expect(errorMessage(err)).toMatch(/certificat de la box a changé/);
});

test('a native failure becomes ApiError status 0', async () => {
  request.mockRejectedValue(new Error('Kergui core is not open'));
  const err = await rejection(api.devices());
  expect(err).toBeInstanceOf(ApiError);
  expect(err.status).toBe(0);
});

test('a non-JSON error body still yields an error', async () => {
  request.mockResolvedValue({ status: 404, body: '404 page not found\n' });
  await expect(api.devices()).rejects.toMatchObject({ status: 404, message: 'HTTP 404' });
});

// --- the journal ------------------------------------------------------------

const journal = KerguiCore.journal as jest.Mock;
const note = KerguiCore.note as jest.Mock;

test('reads the journal through the native module, oldest first', async () => {
  journal.mockResolvedValue(
    JSON.stringify([
      { seq: 1, time: '2026-10-19T14:05:09Z', level: 'INFO', msg: 'core: pointed at https://192.168.1.1' },
      { seq: 2, time: '2026-10-19T14:05:11Z', level: 'WARN', msg: 'HTTP GET /api/devices -> 502 (1.2s)' },
    ]),
  );
  await expect(core.journal(0, 50)).resolves.toEqual([
    { seq: 1, time: '2026-10-19T14:05:09Z', level: 'INFO', msg: 'core: pointed at https://192.168.1.1' },
    { seq: 2, time: '2026-10-19T14:05:11Z', level: 'WARN', msg: 'HTTP GET /api/devices -> 502 (1.2s)' },
  ]);
  expect(journal).toHaveBeenCalledWith(0, 50);
});

test('a journal the core could not encode is empty, not a crash', async () => {
  journal.mockResolvedValue('not json');
  await expect(core.journal()).resolves.toEqual([]);
  journal.mockResolvedValue('{"seq":1}'); // an object, not the expected array
  await expect(core.journal()).resolves.toEqual([]);
});

test('a note that the native module refuses does not break the caller', () => {
  note.mockImplementation(() => {
    throw new Error('core not open');
  });
  expect(() => core.note('warn', 'connexion: essai de https://192.168.1.1')).not.toThrow();
});

// --- what the screens tell the household ------------------------------------

test('a core that cannot be reached is not reported as a generic error', () => {
  const err = new ApiError(0, 'mobilecore: not configured (call Configure or StartDemo first)');
  expect(err.coreUnreachable).toBe(true);
  expect(errorMessage(err)).toBe('Le moteur de l’app n’a pas pu être joint. Fermez puis rouvrez Kergui.');
});

test('a box that never answers is told apart from a box that is not there', () => {
  const timeout = new ApiError(502, 'Get "https://192.168.1.1": context deadline exceeded');
  expect(timeout.timedOut).toBe(true);
  expect(errorMessage(timeout)).toContain('n’a pas répondu à temps');

  const absent = new ApiError(502, 'box unreachable');
  expect(absent.timedOut).toBe(false);
  expect(errorMessage(absent)).toBe('Box injoignable. Êtes-vous sur son Wi-Fi ?');
});
