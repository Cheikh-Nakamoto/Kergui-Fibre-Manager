import KerguiCore from '../../modules/kergui-core';
import { ApiError, api, call } from '../core/client';
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
