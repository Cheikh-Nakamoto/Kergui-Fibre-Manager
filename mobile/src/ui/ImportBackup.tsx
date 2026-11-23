import { useQueryClient } from '@tanstack/react-query';
import * as DocumentPicker from 'expo-document-picker';
import { File } from 'expo-file-system';
import { useState } from 'react';
import { Alert } from 'react-native';
import { List } from 'react-native-paper';

import { api, ApiError } from '../core/client';
import { t } from '../i18n';
import { confirm } from './confirm';

// « Importer une sauvegarde (PC) » (owner request, 2026-11-23): the zip
// `kergui export` writes on the PC — device names, notes, consumption —
// picked from the phone's files and applied to this box. The PC's names win.
export function ImportBackup() {
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);

  const run = async () => {
    const pick = await DocumentPicker.getDocumentAsync({
      type: ['application/zip', 'application/x-zip-compressed', 'application/octet-stream'],
      copyToCacheDirectory: true,
    });
    if (pick.canceled || !pick.assets?.length) return;
    const asset = pick.assets[0];
    if (!(await confirm(t('backup.confirm', { file: asset.name }), t('backup.confirmAction')))) return;
    setBusy(true);
    try {
      const archive = await new File(asset.uri).base64();
      const res = await api.importBackup(archive);
      await qc.invalidateQueries();
      Alert.alert('', t('backup.done', { devices: res.devices, named: res.named, usage: res.usage }));
    } catch (e) {
      const bad = e instanceof ApiError && e.code === 'bad_backup';
      Alert.alert('', bad ? t('backup.bad') : t('backup.failed', { error: e instanceof Error ? e.message : String(e) }));
    } finally {
      setBusy(false);
    }
  };

  return (
    <List.Item
      title={t('backup.title')}
      description={t('backup.hint')}
      descriptionNumberOfLines={3}
      left={(p) => <List.Icon {...p} icon={busy ? 'progress-upload' : 'database-import-outline'} />}
      onPress={busy ? undefined : () => void run()}
      disabled={busy}
      testID="settings-import-backup"
    />
  );
}
