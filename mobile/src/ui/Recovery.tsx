import { useEffect, useState, type ReactNode } from "react";
import { StyleSheet, View } from "react-native";
import {
  ActivityIndicator,
  Button,
  Card,
  HelperText,
  List,
  Text,
  TextInput,
} from "react-native-paper";

import {
  RecoveryError,
  type FoundRelay,
  type FoundRelays,
} from "../core/recovery";
import { hubErrorMessage, noteError, t } from "../i18n";
import type { MemberRole } from "../licensing/api";
import { GoTo } from "./GoTo";
import { KCard } from "./KCard";
import { withMono } from "./Mono";
import { PasswordInput } from "./PasswordInput";
import { fontFamilies, fontStyles } from "./theme";

// The "Téléphone perdu" screen's parts (issue #48). Presentational: the
// signing happens in the core, the uploads in src/features/recovery.ts.

/** A user-facing sentence for a failed recovery. */
export function recoveryErrorMessage(e: unknown): string {
  if (e instanceof RecoveryError) {
    // The core's own text goes to the journal, not to the screen (#119).
    if (e.reason === "other") noteError(e, "récupération");
    return t(`recovery.error.${e.reason}`);
  }
  return hubErrorMessage(e);
}

export type CoOwnerRow = {
  id: string;
  phone: string;
  role: MemberRole;
  /** The member's phone published its key. */
  hasKey: boolean;
  coOwner: boolean;
};

type CoOwnersProps = {
  rows: CoOwnerRow[];
  busy: boolean;
  /** No relay paired yet: the screen says so and offers nothing. */
  noRelay?: boolean;
  onDesignate: (id: string) => void;
  onRemove: (id: string) => void;
};

/** The owner's co-owners: who may take the household over if this phone is lost. */
export function CoOwnersSection({
  rows,
  busy,
  noRelay,
  onDesignate,
  onRemove,
}: CoOwnersProps) {
  let body: ReactNode;
  if (noRelay) {
    body = (
      <View style={[styles.go, styles.pad]} testID="coowner-no-relay">
        <Text variant="bodyMedium" style={styles.hint}>
          {t("recovery.noRelay")}
        </Text>
        <GoTo
          to="/relay"
          icon="access-point-network"
          label={t("go.installRelay")}
          testID="coowner-install-relay"
        />
      </View>
    );
  } else if (rows.length === 0) {
    body = (
      <View style={[styles.go, styles.pad]} testID="coowner-none">
        <Text variant="bodyMedium" style={styles.hint}>
          {t("recovery.noMembers")}
        </Text>
        <GoTo
          to="/household"
          icon="home-account"
          label={t("go.household")}
          testID="coowner-open-household"
        />
      </View>
    );
  } else {
    body = rows.map((r) => (
      <View key={r.id} testID={`coowner-row-${r.id}`}>
        <List.Item
          title={r.phone}
          description={
            !r.hasKey
              ? t("recovery.noKey", { phone: r.phone })
              : r.role !== "admin" && !r.coOwner
                ? t("recovery.viewerOnly")
                : t(r.coOwner ? "recovery.coOwner" : "recovery.notCoOwner")
          }
          descriptionNumberOfLines={3}
          left={(p) => (
            <List.Icon
              {...p}
              icon={r.coOwner ? "account-key" : "account-outline"}
            />
          )}
          testID={`coowner-state-${r.id}`}
        />
        <View style={styles.actions}>
          {r.coOwner ? (
            <Button
              compact
              disabled={busy}
              onPress={() => onRemove(r.id)}
              testID={`coowner-remove-${r.id}`}
            >
              {t("recovery.removeCoOwner")}
            </Button>
          ) : r.hasKey && r.role === "admin" ? (
            <Button
              compact
              mode="contained-tonal"
              disabled={busy}
              onPress={() => onDesignate(r.id)}
              testID={`coowner-add-${r.id}`}
            >
              {t("recovery.designate")}
            </Button>
          ) : null}
        </View>
      </View>
    ));
  }
  return (
    <List.Section
      titleStyle={fontStyles.display}
      title={t("recovery.coOwners")}
      testID="recovery-coowners"
    >
      <Text variant="bodySmall" style={[styles.hint, styles.pad]}>
        {t("recovery.coOwnersHint")}
      </Text>
      {body}
    </List.Section>
  );
}

/** A co-owner's phone: take the household over. */
export function TakeOverSection({
  busy,
  onTakeOver,
}: {
  busy: boolean;
  onTakeOver: () => void;
}) {
  return (
    <List.Section
      titleStyle={fontStyles.display}
      title={t("recovery.takeOver")}
      testID="recovery-takeover-section"
    >
      <Text variant="bodyMedium" style={[styles.hint, styles.pad]}>
        {t("recovery.takeOverHint")}
      </Text>
      <View style={styles.pad}>
        <Button
          mode="contained"
          icon="account-key"
          loading={busy}
          disabled={busy}
          onPress={onTakeOver}
          testID="recovery-takeover"
        >
          {t("recovery.takeOver")}
        </Button>
      </View>
    </List.Section>
  );
}

type AtHomeProps = {
  busy: boolean;
  /** Looks for the household's relay on the Wi-Fi (#117): the core's search, or the demo's. */
  find: () => Promise<FoundRelays>;
  onRecover: (relayAddress: string, boxPassword: string) => void;
};

type Search =
  | { state: "searching" }
  | { state: "found"; relay: FoundRelay }
  | { state: "none"; others: number };

/**
 * The owner's new phone, at home: the relay is found on the Wi-Fi by itself
 * (#117, audit C4), then the box password. The address is typed only as a
 * way out, under « Je ne le trouve pas ».
 */
export function AtHomeSection({ busy, find, onRecover }: AtHomeProps) {
  const [search, setSearch] = useState<Search>({ state: "searching" });
  const [round, setRound] = useState(0);
  const [manual, setManual] = useState(false);
  const [address, setAddress] = useState("");
  const [password, setPassword] = useState("");

  useEffect(() => {
    let live = true;
    find().then(
      (f) => {
        if (!live) return;
        const best = f.relays[0];
        setSearch(
          best
            ? { state: "found", relay: best }
            : { state: "none", others: f.others },
        );
      },
      () => live && setSearch({ state: "none", others: 0 }),
    );
    return () => {
      live = false;
    };
  }, [find, round]);

  const fallback = manual || search.state === "none";
  const typed = fallback ? address.trim() : "";
  const target =
    typed || (search.state === "found" ? search.relay.address : "");
  const askPassword = search.state === "found" || fallback;

  return (
    <List.Section
      titleStyle={fontStyles.display}
      title={t("recovery.atHome")}
      testID="recovery-at-home"
    >
      <View style={[styles.pad, styles.gap]}>
        <Text variant="bodyMedium" style={styles.hint}>
          {t("recovery.atHomeHint")}
        </Text>
        {search.state === "searching" ? (
          <View style={styles.row} testID="recovery-searching">
            <ActivityIndicator size="small" />
            <Text variant="bodyMedium" style={styles.flex}>
              {t("recovery.searching")}
            </Text>
          </View>
        ) : search.state === "found" ? (
          <KCard testID="recovery-relay-found">
            <Card.Content style={styles.gap}>
              <Text variant="titleSmall" accessibilityRole="header">
                {t("recovery.found")}
              </Text>
              <Text variant="bodyMedium">{t("recovery.foundHint")}</Text>
              <Text variant="bodySmall" style={styles.hint}>
                {withMono(
                  t("recovery.foundAt", { address: search.relay.address }),
                  [search.relay.address],
                )}
              </Text>
            </Card.Content>
          </KCard>
        ) : (
          <KCard testID="recovery-relay-none">
            <Card.Content style={styles.gap}>
              <Text variant="titleSmall" accessibilityRole="header">
                {t("recovery.none")}
              </Text>
              <Text variant="bodyMedium">{t("recovery.noneHint")}</Text>
              {search.others > 0 ? (
                <Text variant="bodyMedium">{t("recovery.noneOthers")}</Text>
              ) : null}
            </Card.Content>
            <Card.Actions>
              <Button
                icon="magnify"
                disabled={busy}
                onPress={() => {
                  setSearch({ state: "searching" });
                  setRound((r) => r + 1);
                }}
                testID="recovery-search-again"
              >
                {t("recovery.searchAgain")}
              </Button>
            </Card.Actions>
          </KCard>
        )}
        {fallback ? (
          <View style={styles.gap} testID="recovery-fallback">
            <Text variant="titleSmall" accessibilityRole="header">
              {t("recovery.notFound")}
            </Text>
            <TextInput
              mode="outlined"
              label={t("recovery.relayAddress")}
              value={address}
              onChangeText={setAddress}
              keyboardType="numbers-and-punctuation"
              autoCapitalize="none"
              autoCorrect={false}
              testID="recovery-relay-address"
            />
            <HelperText type="info">
              {t("recovery.relayAddressHint")}
            </HelperText>
          </View>
        ) : (
          <Button
            mode="text"
            icon="help-circle-outline"
            onPress={() => setManual(true)}
            testID="recovery-not-found"
          >
            {t("recovery.notFound")}
          </Button>
        )}
        {askPassword ? (
          <>
            <PasswordInput
              mode="outlined"
              label={t("recovery.boxPassword")}
              value={password}
              onChangeText={setPassword}
              testID="recovery-box-password"
            />
            <Button
              mode="contained"
              icon="home-lock"
              loading={busy}
              disabled={busy || !target || !password}
              onPress={() => onRecover(target, password)}
              testID="recovery-home-submit"
            >
              {t("recovery.recover")}
            </Button>
          </>
        ) : null}
      </View>
    </List.Section>
  );
}

/** The household is this phone's now. */
export function RecoveredNotice() {
  return (
    <Text variant="bodyMedium" style={styles.done} testID="recovery-done">
      {t("recovery.done")}
    </Text>
  );
}

const styles = StyleSheet.create({
  actions: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
    paddingHorizontal: 16,
  },
  hint: { opacity: 0.8 },
  pad: { paddingHorizontal: 16 },
  gap: { gap: 8 },
  go: { gap: 8 },
  row: { flexDirection: "row", alignItems: "center", gap: 12, minHeight: 48 },
  flex: { flex: 1 },
  done: { paddingHorizontal: 16, fontFamily: fontFamilies.body.semibold },
});
