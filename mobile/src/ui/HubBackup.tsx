import { useEffect, useState } from 'react';
import { HelperText, List } from 'react-native-paper';

import { BackupUnavailable, hubBackup, onHubBackupChange } from '../features/hubBackup';
import { noteError, t } from '../i18n';
import { clockTime, formatDay } from '../lib/format';
import { HubError } from '../licensing/api';
import { shareArchive } from './shareArchive';

// The household's backup on the hub (#89): « Sauvegarder maintenant » with the
// last backup's time, and the hub's copy downloaded as the same .zip as
// « Faire une sauvegarde ». In the demo (no hub) the send is refused, said here.

type Said = { kind: 'info' | 'error'; text: string; id: string };

export function HubBackup() {
  const [status, setStatus] = useState<{ available: boolean; lastAt: string | null } | null>(null);
  const [busy, setBusy] = useState<'send' | 'download' | null>(null);
  const [said, setSaid] = useState<Said | null>(null);

  useEffect(() => {
    let live = true;
    const read = () => void hubBackup.status().then((s) => live && setStatus(s), () => undefined);
    read();
    const off = onHubBackupChange(read);
    return () => {
      live = false;
      off();
    };
  }, []);

  const send = async () => {
    setBusy('send');
    setSaid(null);
    try {
      const saved = await hubBackup.sendNow();
      setSaid({ kind: 'info', id: 'settings-hub-backup-done', text: t('hubBackup.done', { day: formatDay(saved.updatedAt), time: clockTime(saved.updatedAt) }) });
    } catch (e) {
      if (e instanceof BackupUnavailable) setSaid({ kind: 'error', id: 'settings-hub-backup-refused', text: t('hubBackup.refused') });
      else if (e instanceof HubError && e.status === 403) setSaid({ kind: 'error', id: 'settings-hub-backup-failed', text: t('hubBackup.memberOnly') });
      else {
        noteError(e, 'sauvegarde Kergui');
        setSaid({ kind: 'error', id: 'settings-hub-backup-failed', text: t('hubBackup.failed') });
      }
    } finally {
      setBusy(null);
    }
  };

  const download = async () => {
    setBusy('download');
    setSaid(null);
    try {
      const copy = await hubBackup.download();
      if (!copy) setSaid({ kind: 'info', id: 'settings-hub-backup-none', text: t('hubBackup.downloadNone') });
      else await shareArchive(copy.archive, copy.name, t('hubBackup.downloadDialog'));
    } catch (e) {
      noteError(e, 'sauvegarde Kergui');
      setSaid({ kind: 'error', id: 'settings-hub-backup-failed', text: t('hubBackup.failed') });
    } finally {
      setBusy(null);
    }
  };

  const description = !status?.available
    ? t('hubBackup.unavailable')
    : status.lastAt
      ? t('hubBackup.last', { day: formatDay(status.lastAt), time: clockTime(status.lastAt) })
      : t('hubBackup.never');

  return (
    <>
      <List.Item
        title={t('hubBackup.title')}
        description={description}
        descriptionNumberOfLines={4}
        left={(p) => <List.Icon {...p} icon={busy === 'send' ? 'progress-upload' : 'cloud-upload-outline'} />}
        onPress={busy ? undefined : () => void send()}
        disabled={!!busy}
        testID="settings-hub-backup"
      />
      {status?.available ? (
        <List.Item
          title={t('hubBackup.downloadTitle')}
          description={t('hubBackup.downloadHint')}
          descriptionNumberOfLines={3}
          left={(p) => <List.Icon {...p} icon={busy === 'download' ? 'progress-download' : 'cloud-download-outline'} />}
          onPress={busy ? undefined : () => void download()}
          disabled={!!busy}
          testID="settings-hub-backup-download"
        />
      ) : null}
      {said ? (
        <HelperText type={said.kind} testID={said.id}>
          {said.text}
        </HelperText>
      ) : null}
    </>
  );
}
