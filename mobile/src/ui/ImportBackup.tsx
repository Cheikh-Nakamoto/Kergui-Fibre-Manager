import { useQueryClient } from '@tanstack/react-query';
import * as DocumentPicker from 'expo-document-picker';
import { File, Paths } from 'expo-file-system';
import * as Sharing from 'expo-sharing';
import { useState } from 'react';
import { Alert } from 'react-native';
import { List } from 'react-native-paper';

import { api, ApiError } from '../core/client';
import { t } from '../i18n';
import { confirm } from './confirm';

// Backups (owner request, 2026-11-23): « Faire une sauvegarde » writes the
// device names, notes and consumption to a .zip the household keeps where it
// wants (the Android share sheet: Drive, Files, WhatsApp, a PC); « Importer
// une sauvegarde » reads one back on a new phone — or one `kergui export`
// made on a PC. The backup's names win.

const message = (e: unknown) => (e instanceof Error ? e.message : String(e));

export function ImportBackup() {
  const qc = useQueryClient();
  const [busy, setBusy] = useState<'export' | 'import' | null>(null);

  const exportBackup = async () => {
    setBusy('export');
    try {
      const { archive, name } = await api.exportBackup();
      const file = new File(Paths.cache, name);
      if (file.exists) file.delete();
      file.create();
      await file.write(archive, { encoding: 'base64' });
      await Sharing.shareAsync(file.uri, { mimeType: 'application/zip', dialogTitle: t('backup.exportDialog') });
    } catch (e) {
      Alert.alert('', t('backup.exportFailed', { error: message(e) }));
    } finally {
      setBusy(null);
    }
  };

  const importBackup = async () => {
    const pick = await DocumentPicker.getDocumentAsync({
      type: ['application/zip', 'application/x-zip-compressed', 'application/octet-stream'],
      copyToCacheDirectory: true,
    });
    if (pick.canceled || !pick.assets?.length) return;
    const asset = pick.assets[0];
    if (!(await confirm(t('backup.confirm', { file: asset.name }), t('backup.confirmAction')))) return;
    setBusy('import');
    try {
      const archive = await new File(asset.uri).base64();
      const res = await api.importBackup(archive);
      await qc.invalidateQueries();
      Alert.alert('', t('backup.done', { devices: res.devices, named: res.named, usage: res.usage }));
    } catch (e) {
      const bad = e instanceof ApiError && e.code === 'bad_backup';
      Alert.alert('', bad ? t('backup.bad') : t('backup.failed', { error: message(e) }));
    } finally {
      setBusy(null);
    }
  };

  return (
    <>
      <List.Item
        title={t('backup.exportTitle')}
        description={t('backup.exportHint')}
        descriptionNumberOfLines={4}
        left={(p) => <List.Icon {...p} icon={busy === 'export' ? 'progress-upload' : 'content-save-outline'} />}
        onPress={busy ? undefined : () => void exportBackup()}
        disabled={!!busy}
        testID="settings-export-backup"
      />
      <List.Item
        title={t('backup.title')}
        description={t('backup.hint')}
        descriptionNumberOfLines={3}
        left={(p) => <List.Icon {...p} icon={busy === 'import' ? 'progress-download' : 'database-import-outline'} />}
        onPress={busy ? undefined : () => void importBackup()}
        disabled={!!busy}
        testID="settings-import-backup"
      />
    </>
  );
}
