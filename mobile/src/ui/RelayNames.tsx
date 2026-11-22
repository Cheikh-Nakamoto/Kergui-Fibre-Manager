import { useState } from 'react';
import { StyleSheet, View } from 'react-native';
import { Button, List, TextInput } from 'react-native-paper';

import type { RemoteRelay } from '../core/remote';
import { t } from '../i18n';
import { confirm } from './confirm';

// The household's relays by the name the owner gave them (issue #57): « PC
// du salon », « Téléphone de Awa ». The name is the relay's label in the
// signed trust bundle, so every phone of the household shows the same one;
// what the relay is stays as the second line. The owner renames a relay
// here, after a confirmation; the signing is the caller's (the core signs
// the next bundle). A name only ever labels a row: no trust decision reads it.

/** A name as the core keeps it: at most 64 characters (trust.MaxLabelLen). */
export const MAX_RELAY_NAME = 64;

export const relayName = (r: Pick<RemoteRelay, 'label'>) => r.label?.trim() || t('relays.unnamed');

export const relayKindName = (kind: string) =>
  t(kind === 'pc' ? 'relays.kindPc' : kind === 'phone' || kind === 'dedicated' ? 'relays.kindPhone' : 'relays.kindOther');

type Props = {
  relays: RemoteRelay[];
  /** The owner's phone: the rows can be renamed. */
  canRename: boolean;
  busy?: boolean;
  onRename?: (agentId: string, name: string) => void | Promise<unknown>;
};

export function RelayNamesSection({ relays, canRename, busy = false, onRename }: Props) {
  const [editing, setEditing] = useState<{ agentId: string; name: string } | null>(null);
  if (relays.length === 0) return null;

  const save = async () => {
    if (!editing || !onRename) return;
    const relay = relays.find((r) => r.agentId === editing.agentId);
    const name = editing.name.trim();
    if (!relay || !name) return;
    if (!(await confirm(t('relays.renameConfirm', { name: relayName(relay), newName: name }), t('relays.renameYes')))) return;
    setEditing(null);
    await onRename(relay.agentId, name);
  };

  return (
    <List.Section title={t('relays.title', { count: relays.length })}>
      {relays.map((r) => (
        <View key={r.agentId} testID={`relay-row-${r.agentId}`}>
          <List.Item
            title={relayName(r)}
            description={relayKindName(r.kind)}
            left={(p) => <List.Icon {...p} icon={r.kind === 'pc' ? 'laptop' : 'cellphone-link'} />}
            right={
              canRename && editing?.agentId !== r.agentId
                ? () => (
                    <Button
                      compact
                      disabled={busy}
                      onPress={() => setEditing({ agentId: r.agentId, name: r.label ?? '' })}
                      testID={`relay-rename-${r.agentId}`}
                    >
                      {t('relays.rename')}
                    </Button>
                  )
                : undefined
            }
          />
          {editing?.agentId === r.agentId ? (
            <View style={styles.edit}>
              <TextInput
                mode="outlined"
                label={t('relays.newName')}
                value={editing.name}
                onChangeText={(name) => setEditing({ agentId: r.agentId, name })}
                maxLength={MAX_RELAY_NAME}
                autoFocus
                selectTextOnFocus
                testID="relay-rename-input"
              />
              <View style={styles.actions}>
                <Button compact onPress={() => setEditing(null)} testID="relay-rename-cancel">
                  {t('common.cancel')}
                </Button>
                <Button compact mode="contained" disabled={busy || !editing.name.trim()} onPress={() => void save()} testID="relay-rename-save">
                  {t('relays.renameSave')}
                </Button>
              </View>
            </View>
          ) : null}
        </View>
      ))}
    </List.Section>
  );
}

const styles = StyleSheet.create({
  edit: { gap: 8, paddingHorizontal: 16 },
  actions: { flexDirection: 'row', justifyContent: 'flex-end', gap: 8 },
});
