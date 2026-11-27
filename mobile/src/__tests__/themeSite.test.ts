// The app wears the website's look (#128): the dark mode is the site as it
// is, token for token (landing/app/globals.css, :root). These values are
// copied from the issue's list, so a drift on either side is caught here.
import { readFileSync } from 'fs';
import { join } from 'path';

import { chrome, fontFamilies, radius, semantic, site, themes, variantFamily } from '../ui/theme';

const SITE = {
  bgDeep: '#050e0b',
  bg: '#07140f',
  surface: '#0d1a15',
  card: '#10201a',
  cardHi: '#0f2a21',
  well: '#0f1714',
  paper: '#f2eee6',
  paperInk: '#13221c',
  paperMuted: '#5b554b',
  ink: '#e8f2ed',
  ink2: '#c5e3d5',
  muted: '#a9c2b6',
  green: '#6fd8ae',
  greenDark: '#a8ebcf',
  brand: '#0b6e4f',
  onGreen: '#06281c',
  amber: '#f2a900',
  line: 'rgba(111,216,174,.14)',
  lineStrong: 'rgba(111,216,174,.35)',
  danger: '#ff8a7a',
  dangerBg: '#3b1717',
  warnBg: '#3a2f12',
  warnInk: '#f2cf73',
};

/** A colour in one spelling: lower case, no spaces, no leading zero. */
const norm = (c: string) => c.toLowerCase().replace(/\s+/g, '').replace(/([,(])0\./g, '$1.');

describe('site tokens', () => {
  it.each(Object.entries(SITE))('%s is the site value', (key, value) => {
    expect(norm(site[key as keyof typeof site])).toBe(norm(value));
  });

  it('matches landing/app/globals.css', () => {
    const css = readFileSync(join(__dirname, '../../../landing/app/globals.css'), 'utf8');
    // bgDeep → --bg-deep, ink2 → --ink-2
    const kebab = (k: string) => '--' + k.replace(/[A-Z0-9]/g, (m) => '-' + m.toLowerCase());
    for (const [key, value] of Object.entries(SITE)) {
      const name = kebab(key);
      const m = css.match(new RegExp(`${name}:\\s*([^;]+);`));
      expect(m && norm(m[1])).toBe(norm(value));
    }
  });
});

describe('dark mode is the site', () => {
  const c = themes('dark').paper.colors;

  it('backgrounds', () => {
    expect(norm(c.background)).toBe(SITE.bg);
    expect(norm(c.surface)).toBe(SITE.surface);
    expect(norm(c.elevation.level1)).toBe(SITE.card); // cards
    expect(norm(c.elevation.level2)).toBe(SITE.cardHi);
    expect(norm(c.surfaceVariant)).toBe(SITE.well);
    expect(norm(String(themes('dark').navigation.colors.background))).toBe(SITE.bg);
  });

  it('text', () => {
    expect(norm(c.onBackground)).toBe(SITE.ink);
    expect(norm(c.onSurface)).toBe(SITE.ink);
    expect(norm(c.onSurfaceVariant)).toBe(SITE.muted);
    expect(norm(c.secondary)).toBe(SITE.ink2);
  });

  it('accents: green buttons on the site dark green, amber', () => {
    expect(norm(c.primary)).toBe(SITE.green);
    expect(norm(c.onPrimary)).toBe(SITE.onGreen);
    expect(norm(c.onPrimaryContainer)).toBe(SITE.greenDark);
    expect(norm(c.inversePrimary)).toBe(SITE.brand);
    expect(norm(c.tertiary)).toBe(SITE.amber);
  });

  it('lines: ghost buttons and inputs on line-strong, dividers on line', () => {
    expect(norm(c.outline)).toBe(norm(SITE.lineStrong));
    expect(norm(c.outlineVariant)).toBe(norm(SITE.line));
  });

  it('states', () => {
    expect(norm(c.error)).toBe(SITE.danger);
    expect(norm(c.errorContainer)).toBe(SITE.dangerBg);
    expect(norm(c.tertiaryContainer)).toBe(SITE.warnBg);
    expect(norm(c.onTertiaryContainer)).toBe(SITE.warnInk);
    const s = semantic.dark;
    expect(norm(s.connected.color)).toBe(SITE.green);
    expect(norm(s.restore.color)).toBe(SITE.green);
    expect(norm(s.blocked.color)).toBe(SITE.danger);
    expect(norm(s.blocked.container)).toBe(SITE.dangerBg);
    expect(norm(s.attention.color)).toBe(SITE.amber);
    expect(norm(s.attention.container)).toBe(SITE.warnBg);
    expect(norm(s.attention.onContainer)).toBe(SITE.warnInk);
  });

  it('chrome: the site header, no solid green block', () => {
    const bars = chrome.dark;
    // The site's header and the system bars around it: --bg.
    expect(norm(bars.background)).toBe(SITE.bg);
    expect(norm(bars.border)).toBe(norm(SITE.line));
    expect(norm(bars.onBackground)).toBe(SITE.ink);
    expect(norm(bars.active)).toBe(SITE.green);
    expect(norm(bars.inactive)).toBe(SITE.muted);
    expect(bars.statusBar).toBe('light');
  });
});

describe('light mode is the paper band', () => {
  const c = themes('light').paper.colors;
  it('paper background, paper ink, the brand green', () => {
    expect(norm(c.background)).toBe(SITE.paper);
    expect(norm(c.onBackground)).toBe(SITE.paperInk);
    expect(norm(c.onSurface)).toBe(SITE.paperInk);
    expect(norm(c.onSurfaceVariant)).toBe(SITE.paperMuted);
    expect(norm(c.primary)).toBe(SITE.brand);
    expect(norm(semantic.light.connected.color)).toBe(SITE.brand);
    expect(norm(semantic.light.attention.color)).toBe(SITE.amber);
    expect(chrome.light.statusBar).toBe('dark');
  });
  it('follows the phone: light unless the phone is dark', () => {
    expect(themes('light').paper.dark).toBe(false);
    expect(themes(null).paper.dark).toBe(false);
    expect(themes('dark').paper.dark).toBe(true);
  });
});

describe('shapes', () => {
  it('the site radii', () => {
    expect(radius).toEqual({ sm: 6, md: 10, lg: 12 });
  });
});

describe('fonts', () => {
  const fonts = themes('dark').paper.fonts as unknown as Record<string, { fontFamily: string; fontWeight?: string }>;

  it('Chakra Petch on titles, Inter on text', () => {
    for (const v of ['displaySmall', 'headlineSmall', 'titleLarge', 'titleMedium', 'titleSmall'])
      expect(fonts[v].fontFamily).toMatch(/^ChakraPetch_/);
    for (const v of ['bodyLarge', 'bodyMedium', 'bodySmall', 'labelLarge', 'labelMedium', 'labelSmall'])
      expect(fonts[v].fontFamily).toMatch(/^Inter_/);
    expect(variantFamily('bodyMedium', 400)).toBe(fontFamilies.body.regular);
  });

  it('no fontWeight on a custom family: the weight is in the file name', () => {
    for (const f of Object.values(fonts)) expect(f.fontWeight).toBeUndefined();
  });

  it('every family the theme names is loaded by the root layout', () => {
    const layout = readFileSync(join(__dirname, '../app/_layout.tsx'), 'utf8');
    const loaded = layout.slice(layout.indexOf('useFonts({'), layout.indexOf('});', layout.indexOf('useFonts({')));
    for (const group of Object.values(fontFamilies))
      for (const family of Object.values(group)) expect(loaded).toContain(family);
  });

  it('only the weights the site uses: Chakra 500/600/700, JetBrains Mono 500/600', () => {
    expect(Object.values(fontFamilies.display)).toEqual(['ChakraPetch_500Medium', 'ChakraPetch_600SemiBold', 'ChakraPetch_700Bold']);
    expect(Object.values(fontFamilies.mono)).toEqual(['JetBrainsMono_500Medium', 'JetBrainsMono_600SemiBold']);
  });
});
