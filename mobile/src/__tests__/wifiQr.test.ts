// The Wi-Fi QR code (#123, audit H8): the format phone cameras read to join a
// network, WIFI:T:WPA;S:<name>;P:<password>;; with \ ; , : " escaped by a
// backslash in the name and the password.
import { wifiQrValue } from '../lib/wifiQr';

test('a plain network and password', () => {
  expect(wifiQrValue({ name: 'MonWiFi-2G', password: 'nouveau-secret' })).toBe('WIFI:T:WPA;S:MonWiFi-2G;P:nouveau-secret;;');
});

test.each([
  ['\\', '\\\\'],
  [';', '\\;'],
  [',', '\\,'],
  [':', '\\:'],
  ['"', '\\"'],
])('%s is escaped in the name and in the password', (raw, escaped) => {
  expect(wifiQrValue({ name: `a${raw}b`, password: `pass${raw}word` })).toBe(`WIFI:T:WPA;S:a${escaped}b;P:pass${escaped}word;;`);
});

test('every special character at once, the backslash escaped only once', () => {
  expect(wifiQrValue({ name: 'Chez "Awa": 2,4 GHz', password: 'a\\;b,c:d"e' })).toBe(
    'WIFI:T:WPA;S:Chez \\"Awa\\"\\: 2\\,4 GHz;P:a\\\\\\;b\\,c\\:d\\"e;;',
  );
});

test('a hidden network says so, for the phone to look for it', () => {
  expect(wifiQrValue({ name: 'Maison', password: '12345678', hidden: true })).toBe('WIFI:T:WPA;S:Maison;P:12345678;H:true;;');
});

test('spaces and accents in the name are kept as they are', () => {
  expect(wifiQrValue({ name: 'Maison Ndèye', password: '12345678' })).toBe('WIFI:T:WPA;S:Maison Ndèye;P:12345678;;');
});
