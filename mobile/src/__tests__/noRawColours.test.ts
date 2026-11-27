// Screens never pick a raw colour (#75, #128): they ask the theme for a
// meaning (useSemantic, useChrome, the Paper roles, qr). And a weight is
// chosen by its family's file (fontFamilies), never with fontWeight: on
// Android a custom family with fontWeight is not mapped to a heavier file.
import { readdirSync, readFileSync, statSync } from 'fs';
import { join, relative } from 'path';

const SRC = join(__dirname, '..');
// The theme is where the colours live.
const ALLOWED = new Set(['ui/theme.ts']);

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return name === '__tests__' ? [] : files(path);
    return /\.tsx?$/.test(name) ? [path] : [];
  });
}

/** The code without its comments: an issue number (#113) is not a colour. */
function code(path: string): string {
  return readFileSync(path, 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/(^|[^:'"`])\/\/.*$/gm, '$1');
}

const sources = ['app', 'ui', 'features']
  .flatMap((d) => files(join(SRC, d)))
  .map((p) => ({ name: relative(SRC, p), text: code(p) }))
  .filter((f) => !ALLOWED.has(f.name));

const HEX = /['"`]#[0-9a-fA-F]{3,8}['"`]/;
const FUNC = /['"`](rgba?|hsla?)\(/;
const NAMED = /\b(color|backgroundColor|borderColor|tintColor|textColor|iconColor|selectedColor|rippleColor)\s*[:=]\s*\{?\s*['"](white|black|red|green|blue|orange|yellow|gr[ae]y|silver)['"]/;

describe('no raw colour in the screens', () => {
  it('finds the screens', () => {
    expect(sources.length).toBeGreaterThan(20);
  });

  it.each(sources.map((f) => [f.name, f.text]))('%s', (_name, text) => {
    expect(text).not.toMatch(HEX);
    expect(text).not.toMatch(FUNC);
    expect(text).not.toMatch(NAMED);
  });
});

describe('no fontWeight on a custom family', () => {
  it.each(sources.map((f) => [f.name, f.text]))('%s', (_name, text) => {
    expect(text).not.toMatch(/fontWeight\s*:/);
    // The platform's own monospace is not the site's JetBrains Mono.
    expect(text).not.toMatch(/['"](monospace|Menlo|Inter_\w+|ChakraPetch_\w+|JetBrainsMono_\w+)['"]/);
  });
});
