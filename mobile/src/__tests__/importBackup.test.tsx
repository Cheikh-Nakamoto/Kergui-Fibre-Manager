/* eslint-disable import/first -- the mocks must come before the modules they replace */
// « Importer une sauvegarde (PC) » (owner request, 2026-11-23): the zip that
// `kergui export` wrote is picked, confirmed, and sent to the core.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Alert } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type TestInstance } from 'test-renderer';

const mockPick = jest.fn();
jest.mock('expo-document-picker', () => ({ getDocumentAsync: (...a: unknown[]) => mockPick(...a) }));
jest.mock('expo-file-system', () => ({
  File: class {
    base64 = async () => 'UEsDBA==';
  },
}));
const mockConfirm = jest.fn();
jest.mock('../ui/confirm', () => ({ confirm: (...a: unknown[]) => mockConfirm(...a) }));

import KerguiCore from '../../modules/kergui-core';
import { ImportBackup } from '../ui/ImportBackup';

const request = KerguiCore.request as jest.Mock;

async function pressImport() {
  const r = createRoot();
  await act(async () =>
    r.render(
      <QueryClientProvider client={new QueryClient()}>
        <PaperProvider>
          <ImportBackup />
        </PaperProvider>
      </QueryClientProvider>,
    ),
  );
  const item = r.container.queryAll(
    (n: TestInstance) => n.props.testID === 'settings-import-backup' && typeof (n.props.onPress ?? n.props.onClick) === 'function',
  )[0];
  await act(async () => (item.props.onPress ?? item.props.onClick)());
  return r;
}

beforeEach(() => {
  request.mockReset();
  mockPick.mockReset();
  mockConfirm.mockReset();
  jest.spyOn(Alert, 'alert').mockImplementation(() => undefined);
});

test('the picked zip is sent to the core and the result is said', async () => {
  mockPick.mockResolvedValue({ canceled: false, assets: [{ uri: 'file:///cache/k.zip', name: 'kergui-sauvegarde.zip' }] });
  mockConfirm.mockResolvedValue(true);
  request.mockResolvedValue({ status: 200, body: JSON.stringify({ devices: 98, named: 34, usage: 133 }) });
  await pressImport();
  expect(request).toHaveBeenCalledWith('POST', '/api/import', JSON.stringify({ archive: 'UEsDBA==' }));
  expect(Alert.alert).toHaveBeenCalledWith('', expect.stringContaining('98 appareils, dont 34 nommés'));
});

test('nothing is sent when the household cancels', async () => {
  mockPick.mockResolvedValue({ canceled: false, assets: [{ uri: 'file:///cache/k.zip', name: 'k.zip' }] });
  mockConfirm.mockResolvedValue(false);
  await pressImport();
  expect(request).not.toHaveBeenCalled();
});

test('a file that is not a backup is said so', async () => {
  mockPick.mockResolvedValue({ canceled: false, assets: [{ uri: 'file:///cache/p.zip', name: 'photos.zip' }] });
  mockConfirm.mockResolvedValue(true);
  request.mockResolvedValue({ status: 400, body: JSON.stringify({ error: 'not a backup', code: 'bad_backup' }) });
  await pressImport();
  expect(Alert.alert).toHaveBeenCalledWith('', expect.stringContaining('pas une sauvegarde Kergui'));
});
