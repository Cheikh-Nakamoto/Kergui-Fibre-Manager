import { File, Paths } from 'expo-file-system';
import * as Sharing from 'expo-sharing';

/** Writes a base64 zip to the cache and opens the share sheet (Drive, Files, WhatsApp, a PC). */
export async function shareArchive(archive: string, name: string, dialogTitle: string): Promise<void> {
  const file = new File(Paths.cache, name);
  if (file.exists) file.delete();
  file.create();
  await file.write(archive, { encoding: 'base64' });
  await Sharing.shareAsync(file.uri, { mimeType: 'application/zip', dialogTitle });
}
