// The Wi-Fi QR code (#123, audit H8): the format a phone's camera reads to
// join a network (the WIFI: syntax Android and iOS share). \ ; , : and "
// are escaped by a backslash in the name and the password. Built from what
// was just typed, on screen only: never stored nor logged.

const escape = (s: string) => s.replace(/[\\;,:"]/g, '\\$&');

/** WIFI:T:WPA;S:<name>;P:<password>;; (with H:true; for a hidden network). */
export function wifiQrValue({ name, password, hidden = false }: { name: string; password: string; hidden?: boolean }): string {
  return `WIFI:T:WPA;S:${escape(name)};P:${escape(password)};${hidden ? 'H:true;' : ''};`;
}
