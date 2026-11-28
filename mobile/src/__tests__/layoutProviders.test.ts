// The query client must wrap Paper (#108): a Paper Portal — the Wi-Fi
// panel opened by « Gérer », the confirmations — draws at the
// PaperProvider's level, so with Paper outside the query client its hooks
// threw « No QueryClient set » and the app closed (mobile-ci run
// 46109520986). Jest renders screens with their own providers, so this
// order is checked on the root layout itself.
import { readFileSync } from 'fs';
import { join } from 'path';

test('QueryClientProvider wraps PaperProvider in the root layout', () => {
  const src = readFileSync(join(__dirname, '../app/_layout.tsx'), 'utf8');
  const query = src.indexOf('<QueryClientProvider');
  const paper = src.indexOf('<PaperProvider');
  expect(query).toBeGreaterThan(-1);
  expect(paper).toBeGreaterThan(-1);
  expect(query).toBeLessThan(paper);
  expect(src.lastIndexOf('</QueryClientProvider>')).toBeGreaterThan(src.lastIndexOf('</PaperProvider>'));
});
