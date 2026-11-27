import { useMemo } from 'react';
import { View } from 'react-native';
import { toQR } from 'toqr';

import { qr } from './theme';

// A QR code drawn with plain views: no SVG module, no image. Black on white
// in both themes, with the four-module quiet zone scanners expect.

const QUIET = 4;

/** The QR modules of value as rows of booleans (true = dark). */
export function qrRows(value: string): boolean[][] {
  const cells = toQR(value);
  const n = Math.round(Math.sqrt(cells.length));
  const rows: boolean[][] = [];
  for (let y = 0; y < n; y++) {
    const row: boolean[] = [];
    for (let x = 0; x < n; x++) row.push(cells[y * n + x] === 1);
    rows.push(row);
  }
  return rows;
}

export function QrCode({ value, size = 240, testID }: { value: string; size?: number; testID?: string }) {
  const rows = useMemo(() => qrRows(value), [value]);
  const cell = Math.max(1, Math.floor(size / (rows.length + 2 * QUIET)));
  return (
    <View
      testID={testID}
      accessible
      accessibilityRole="image"
      style={{ backgroundColor: qr.light, padding: cell * QUIET, alignSelf: 'center' }}
    >
      {rows.map((row, y) => (
        <View key={y} style={{ flexDirection: 'row' }}>
          {row.map((dark, x) => (
            <View key={x} style={{ width: cell, height: cell, backgroundColor: dark ? qr.dark : qr.light }} />
          ))}
        </View>
      ))}
    </View>
  );
}
