import { CameraView, useCameraPermissions } from 'expo-camera';
import Constants from 'expo-constants';
import { Stack } from 'expo-router';
import { useCallback, useEffect, useRef, useState } from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Button, HelperText, Text, TextInput } from 'react-native-paper';

import { owner } from '../core/owner';
import { parseInvite, relayPhone, type RelayStatus } from '../core/relayPhone';
import { hubErrorMessage, t } from '../i18n';
import { config } from '../licensing/config';
import { useSession } from '../session/store';

// This phone becomes the household's relay (issue #45), from the welcome
// screen ("Être un relais") or Réglages: it scans the QR the owner's phone
// shows (or the code typed under it when the camera is refused), shows its
// own six digits, waits for the owner to confirm them on their screen, then
// asks for the notifications and the location (to read the Wi-Fi's name)
// and starts the relay service. The box password comes sealed from the
// owner's phone: this phone never logs in to the box by hand.
//
// In the demo there is no hub: the core plays the owner on a QR of its own,
// the confirmation is a button, and no service is started.

/** How often the waiting screen asks the hub whether the owner confirmed. */
export const TRUST_POLL_MS = 3000;
/** How often the running screen reads the relay's state. */
export const STATUS_POLL_MS = 5000;

type Step =
  | { at: 'checking' }
  | { at: 'scan' }
  | { at: 'code'; code: string | null }
  | { at: 'confirmed' }
  | { at: 'active' };

/** "042 917": six digits read in two groups, as on the owner's screen. */
const groupCode = (code: string) => `${code.slice(0, 3)} ${code.slice(3)}`;

const deviceName = () => (Constants.deviceName ?? '').slice(0, 64) || 'Téléphone relais';

function Flow({ demo }: { demo: boolean }) {
  const [step, setStep] = useState<Step>({ at: demo ? 'scan' : 'checking' });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [camera, setCamera] = useState(false);
  const [denied, setDenied] = useState(false);
  const [manual, setManual] = useState('');
  const [status, setStatus] = useState<RelayStatus | null>(null);
  const [location, setLocation] = useState(true);
  const [, requestCamera] = useCameraPermissions();
  const scanned = useRef(false);

  const run = async (work: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await work();
    } catch (e) {
      setError(hubErrorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  // Where this phone is: paired and confirmed, waiting for the owner, or new.
  useEffect(() => {
    if (demo) return;
    void (async () => {
      try {
        if (!(await relayPhone.enrolled())) return setStep({ at: 'scan' });
        if (!(await relayPhone.trusted())) return setStep({ at: 'code', code: null });
        setStep({ at: relayPhone.enabled() ? 'active' : 'confirmed' });
      } catch {
        setStep({ at: 'scan' });
      }
    })();
  }, [demo]);

  const submit = (raw: string) =>
    run(async () => {
      const invite = parseInvite(raw);
      if (!invite) {
        scanned.current = false;
        setError(t('relayPhone.badCode'));
        return;
      }
      setCamera(false);
      const enrolled = await relayPhone.enroll(config.hubUrl, invite, deviceName());
      setStep({ at: 'code', code: enrolled.code });
    });

  const demoScan = () =>
    run(async () => {
      const invite = await owner.invite(`DEMO-${Date.now().toString(36)}`);
      const relay = await owner.demoRelay(invite);
      setStep({ at: 'code', code: relay.code });
    });

  const openCamera = () =>
    run(async () => {
      const p = await requestCamera();
      if (!p.granted) {
        setDenied(true);
        return;
      }
      scanned.current = false;
      setDenied(false);
      setCamera(true);
    });

  // Waiting for "Oui, c'est le même code" on the owner's phone.
  const waiting = step.at === 'code';
  useEffect(() => {
    if (!waiting || demo) return;
    let stopped = false;
    const check = async () => {
      try {
        if ((await relayPhone.trusted()) && !stopped) setStep({ at: 'confirmed' });
      } catch {
        // The hub is momentarily out of reach: the next round asks again.
      }
    };
    const timer = setInterval(() => void check(), TRUST_POLL_MS);
    return () => {
      stopped = true;
      clearInterval(timer);
    };
  }, [waiting, demo]);

  const start = () =>
    run(async () => {
      if (!demo) {
        const granted = await relayPhone.askPermissions();
        setLocation(granted.location);
        await relayPhone.start();
      }
      setStep({ at: 'active' });
    });

  const stop = () =>
    run(async () => {
      if (!demo) await relayPhone.stop();
      setStatus(null);
      setStep({ at: 'confirmed' });
    });

  const refresh = useCallback(async () => {
    try {
      setStatus(await relayPhone.status());
      setLocation(await relayPhone.locationGranted());
    } catch {
      // Shown again on the next round.
    }
  }, []);
  const active = step.at === 'active';
  useEffect(() => {
    if (!active || demo) return;
    const first = setTimeout(() => void refresh(), 0);
    const timer = setInterval(() => void refresh(), STATUS_POLL_MS);
    return () => {
      clearTimeout(first);
      clearInterval(timer);
    };
  }, [active, demo, refresh]);

  const body = () => {
    switch (step.at) {
      case 'checking':
        return null;
      case 'scan':
        return (
          <View style={styles.gap} testID="relay-phone-intro">
            <Text variant="titleMedium">{t('relayPhone.introTitle')}</Text>
            <Text variant="bodyMedium">{t('relayPhone.intro')}</Text>
            <Text variant="bodyMedium" style={styles.hint}>
              {t('relayPhone.introHow')}
            </Text>
            {camera ? (
              <View style={styles.gap}>
                <Text variant="bodyMedium">{t('relayPhone.scanHint')}</Text>
                <CameraView
                  style={styles.camera}
                  facing="back"
                  barcodeScannerSettings={{ barcodeTypes: ['qr'] }}
                  onBarcodeScanned={({ data }) => {
                    if (scanned.current) return;
                    scanned.current = true;
                    void submit(data);
                  }}
                  testID="relay-camera"
                />
                <Button mode="outlined" onPress={() => setCamera(false)} testID="relay-scan-cancel">
                  {t('relayPhone.cancelScan')}
                </Button>
              </View>
            ) : demo ? (
              <Button mode="contained" icon="qrcode-scan" onPress={() => void demoScan()} loading={busy} disabled={busy} testID="relay-demo-qr">
                {t('relayPhone.demoScan')}
              </Button>
            ) : (
              <Button mode="contained" icon="qrcode-scan" onPress={() => void openCamera()} disabled={busy} testID="relay-scan">
                {t('relayPhone.scan')}
              </Button>
            )}
            {denied ? (
              <HelperText type="info" testID="relay-camera-denied">
                {t('relayPhone.cameraDenied')}
              </HelperText>
            ) : null}
            <TextInput
              mode="outlined"
              label={t('relayPhone.manualLabel')}
              value={manual}
              onChangeText={setManual}
              autoCapitalize="none"
              autoCorrect={false}
              testID="relay-manual-code"
            />
            <Button
              mode="outlined"
              onPress={() => void submit(manual)}
              loading={busy && !camera}
              disabled={busy || !manual.trim() || demo}
              testID="relay-manual-submit"
            >
              {t('relayPhone.manualSubmit')}
            </Button>
          </View>
        );
      case 'code':
        return (
          <View style={styles.gap} testID="relay-waiting">
            {step.code ? (
              <>
                <Text variant="titleMedium">{t('relayPhone.myCodeTitle')}</Text>
                <Text
                  variant="displaySmall"
                  style={styles.code}
                  accessibilityLabel={t('relay.codeLabel', { code: step.code.split('').join(' ') })}
                  testID="relay-my-code"
                >
                  {groupCode(step.code)}
                </Text>
                <Text variant="bodyMedium">{t('relayPhone.myCode')}</Text>
              </>
            ) : (
              <Text variant="bodyMedium">{t('relayPhone.waitingNoCode')}</Text>
            )}
            <Text variant="bodyMedium" style={styles.hint}>
              {t('relayPhone.waiting')}
            </Text>
            {demo ? (
              <Button mode="contained" icon="check" onPress={() => setStep({ at: 'confirmed' })} testID="relay-demo-confirm">
                {t('relayPhone.demoConfirm')}
              </Button>
            ) : (
              <Button mode="text" onPress={() => setStep({ at: 'scan' })} testID="relay-restart">
                {t('relayPhone.restart')}
              </Button>
            )}
          </View>
        );
      case 'confirmed':
        return (
          <View style={styles.gap} testID="relay-confirmed">
            <Text variant="titleMedium">{t('relayPhone.confirmedTitle')}</Text>
            <Text variant="bodyMedium">{t('relayPhone.password')}</Text>
            <Text variant="bodyMedium" style={styles.hint}>
              {t('relayPhone.permissions')}
            </Text>
            <Button mode="contained" icon="play" onPress={() => void start()} loading={busy} disabled={busy} testID="relay-start-service">
              {t('relayPhone.startButton')}
            </Button>
          </View>
        );
      case 'active': {
        const state = demo ? 'active' : (status?.state ?? 'starting');
        const boxOnly = !demo && (status?.check === 'box-identity' || !location);
        return (
          <View style={styles.gap} testID="relay-active">
            <Text variant="titleMedium">{t('relayPhone.activeTitle')}</Text>
            <Text variant="bodyLarge" testID={`relay-state-${state}`}>
              {t(`relayPhone.state.${state}`)}
            </Text>
            {boxOnly ? (
              <Text variant="bodyMedium" style={styles.hint} testID="relay-location-off">
                {t('relayPhone.locationOff')}
              </Text>
            ) : null}
            <Text variant="bodySmall" style={styles.hint}>
              {t('relayPhone.battery')}
            </Text>
            <Button mode="outlined" icon="stop" onPress={() => void stop()} disabled={busy} testID="relay-stop">
              {t('relayPhone.stopButton')}
            </Button>
          </View>
        );
      }
    }
  };

  return (
    <ScrollView contentContainerStyle={styles.body} keyboardShouldPersistTaps="handled">
      {demo ? (
        <Text variant="bodySmall" style={styles.hint} testID="relay-phone-demo">
          {t('relayPhone.demo')}
        </Text>
      ) : null}
      {body()}
      {error ? (
        <HelperText type="error" testID="relay-phone-error">
          {error}
        </HelperText>
      ) : null}
    </ScrollView>
  );
}

export default function BecomeRelayScreen() {
  const demo = useSession((s) => s.session?.mode === 'demo');
  const body = () => {
    if (demo) return <Flow demo />;
    if (!relayPhone.supported()) {
      return (
        <View style={styles.body}>
          <Text variant="bodyMedium" testID="relay-phone-unsupported">
            {t('relayPhone.unsupported')}
          </Text>
        </View>
      );
    }
    if (!config.hubUrl) {
      return (
        <View style={styles.body}>
          <Text variant="bodyMedium">{t('pro.unconfigured')}</Text>
        </View>
      );
    }
    return <Flow demo={false} />;
  };
  return (
    <>
      <Stack.Screen options={{ headerShown: true, title: t('relayPhone.title') }} />
      {body()}
    </>
  );
}

const styles = StyleSheet.create({
  body: { padding: 16, gap: 12 },
  gap: { gap: 12 },
  hint: { opacity: 0.8 },
  code: { textAlign: 'center', letterSpacing: 4, fontVariant: ['tabular-nums'] },
  camera: { width: '100%', aspectRatio: 1, borderRadius: 12, overflow: 'hidden' },
});
