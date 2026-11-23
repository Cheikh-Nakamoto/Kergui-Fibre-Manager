/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Backups (owner request, 2026-11-23): « Faire une sauvegarde » shares the
// core's archive; « Importer une sauvegarde » sends a picked one back.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Alert } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type TestInstance } from 'test-renderer';

const mockPick = jest.fn();
jest.mock('expo-document-picker', () => ({ getDocumentAsync: (...a: unknown[]) => mockPick(...a) }));
const mockWrites: { args: unknown[] }[] = [];
jest.mock('expo-file-system', () => ({
  Paths: { cache: 'cache' },
  File: class {
    uri = 'file:///cache/kergui-sauvegarde.zip';
    exists = false;
    base64 = async () => 'UEsDBA==';
    create = () => undefined;
    delete = () => undefined;
    write = async (...args: unknown[]) => void mockWrites.push({ args });
  },
}));
const mockShare = jest.fn();
jest.mock('expo-sharing', () => ({ shareAsync: (...a: unknown[]) => mockShare(...a) }));
const mockConfirm = jest.fn();
jest.mock('../ui/confirm', () => ({ confirm: (...a: unknown[]) => mockConfirm(...a) }));

import KerguiCore from '../../modules/kergui-core';
import { ImportBackup } from '../ui/ImportBackup';

const request = KerguiCore.request as jest.Mock;

async function press(testID: string) {
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
    (n: TestInstance) => n.props.testID === testID && typeof (n.props.onPress ?? n.props.onClick) === 'function',
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
  await press('settings-import-backup');
  expect(request).toHaveBeenCalledWith('POST', '/api/import', JSON.stringify({ archive: 'UEsDBA==' }));
  expect(Alert.alert).toHaveBeenCalledWith('', expect.stringContaining('98 appareils, dont 34 nommés'));
});

test('nothing is sent when the household cancels', async () => {
  mockPick.mockResolvedValue({ canceled: false, assets: [{ uri: 'file:///cache/k.zip', name: 'k.zip' }] });
  mockConfirm.mockResolvedValue(false);
  await press('settings-import-backup');
  expect(request).not.toHaveBeenCalled();
});

test('a file that is not a backup is said so', async () => {
  mockPick.mockResolvedValue({ canceled: false, assets: [{ uri: 'file:///cache/p.zip', name: 'photos.zip' }] });
  mockConfirm.mockResolvedValue(true);
  request.mockResolvedValue({ status: 400, body: JSON.stringify({ error: 'not a backup', code: 'bad_backup' }) });
  await press('settings-import-backup');
  expect(Alert.alert).toHaveBeenCalledWith('', expect.stringContaining('pas une sauvegarde Kergui'));
});

test('« Faire une sauvegarde » writes the core archive and opens the share sheet', async () => {
  request.mockResolvedValue({ status: 200, body: JSON.stringify({ archive: 'UEsDBA==', name: 'kergui-sauvegarde-2026-11-23.zip' }) });
  await press('settings-export-backup');
  expect(request).toHaveBeenCalledWith('GET', '/api/export', '');
  expect(mockWrites[0].args).toEqual(['UEsDBA==', { encoding: 'base64' }]);
  expect(mockShare).toHaveBeenCalledWith('file:///cache/kergui-sauvegarde.zip', expect.objectContaining({ mimeType: 'application/zip' }));
});
