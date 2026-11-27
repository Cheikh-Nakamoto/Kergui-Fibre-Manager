// One theme for the whole app. Paper (buttons, cards, lists) and the router's
// navigation (headers, tab bar, screen background) used to pick their light /
// dark mode separately: with the phone in dark mode, Paper drew light text and
// dark cards while the navigation kept white screens, so most text was
// unreadable. Both now come from the same scheme and the same colours.
//
// The colours, fonts and shapes are the website's (#128). The fonts are
// bundled with the app: React Native otherwise inherits the phone's system
// font, and some phones (Samsung "font style") ship decorative ones.
import { DarkTheme as NavDark, DefaultTheme as NavLight, type Theme as NavTheme } from 'expo-router';
import { configureFonts, MD3DarkTheme, MD3LightTheme, useTheme, type MD3Theme } from 'react-native-paper';

// The website's design tokens (landing/app/globals.css, :root), the source of
// both palettes (#128). The dark mode is the site as it is; the light mode
// (the sun, outdoors) is recoloured from the site's light « paper » band.
// themeSite.test.ts holds the dark palette to these values.
export const site = {
  // surfaces, darkest to lightest
  bgDeep: '#050E0B',
  bg: '#07140F',
  surface: '#0D1A15',
  card: '#10201A',
  cardHi: '#0F2A21',
  well: '#0F1714',
  // the light « paper » band
  paper: '#F2EEE6',
  paperInk: '#13221C',
  paperMuted: '#5B554B',
  // text
  ink: '#E8F2ED',
  ink2: '#C5E3D5',
  muted: '#A9C2B6',
  // accents
  green: '#6FD8AE',
  greenDark: '#A8EBCF',
  brand: '#0B6E4F',
  onGreen: '#06281C',
  amber: '#F2A900',
  amberSoft: '#2A2208',
  // lines
  line: 'rgba(111,216,174,0.14)',
  lineStrong: 'rgba(111,216,174,0.35)',
  // states
  danger: '#FF8A7A',
  dangerBg: '#3B1717',
  dangerInk: '#F4B4B4',
  warnBg: '#3A2F12',
  warnInk: '#F2CF73',
} as const;

// The paper band's own additions, in the same spirit: a card a shade lighter
// than the paper, a well a shade deeper, lines tinted with the brand green.
const paperBand = {
  card: '#FAF7F1',
  cardHi: '#F7F3EB',
  well: '#E7E1D6',
  // Inputs and outlined buttons in the sun: a solid border at 3:1 on the
  // paper (the site's translucent line reads 2:1 there).
  outline: '#6B8A7C',
  line: 'rgba(11,110,79,0.18)',
} as const;

/** The site's shapes: radii of 6 (chips, small marks), 10 (cards, buttons), 12 (big blocks). */
export const radius = { sm: 6, md: 10, lg: 12 } as const;

// Semantic colours (#75). Screens never pick a raw colour: they ask for the
// meaning, and the meaning carries an icon and a word next to it.
//  - connected: on the network, legitimately (green)
//  - restore: gives access back, safe and free (green)
//  - blocked: blocked by the filter, and every action that blocks (red)
//  - cut: cut off the Wi-Fi, told apart from a block (orange-red)
//  - attention: « look at this »: new, not approved, pending (amber)
//  - neutral: news from Kergui (an update, a message, an answer): nothing
//    about the network, so not green (#125, audit L4); the theme's muted ink
// color/onColor fill small areas (badges, chips, buttons); container/
// onContainer tint cards and banners, so screens stay calm. Every pair is
// held to WCAG AA by themeContrast.test.ts.
export const semanticRoles = ['connected', 'restore', 'blocked', 'cut', 'attention', 'neutral'] as const;
export type SemanticRole = (typeof semanticRoles)[number];
export type Tone = { color: string; onColor: string; container: string; onContainer: string };
export type Semantic = Record<SemanticRole, Tone>;

const greenLight: Tone = { color: site.brand, onColor: '#FFFFFF', container: '#D6ECDF', onContainer: site.onGreen };
// The site's green button: green on its dark ink; the site's green card.
const greenDark: Tone = { color: site.green, onColor: site.onGreen, container: site.cardHi, onContainer: site.greenDark };

export const semantic: Record<'light' | 'dark', Semantic> = {
  light: {
    connected: greenLight,
    restore: greenLight,
    blocked: { color: '#B3261E', onColor: '#FFFFFF', container: '#F9DCD6', onContainer: '#410E0B' },
    cut: { color: '#B3400B', onColor: '#FFFFFF', container: '#FBDCC8', onContainer: '#3A0B00' },
    attention: { color: site.amber, onColor: '#261A00', container: '#F6E2A8', onContainer: site.warnBg },
    neutral: { color: site.paperMuted, onColor: '#FFFFFF', container: paperBand.well, onContainer: site.paperInk },
  },
  // The site's state colours: danger on its dark red, the warning band.
  dark: {
    connected: greenDark,
    restore: greenDark,
    blocked: { color: site.danger, onColor: site.dangerBg, container: site.dangerBg, onContainer: site.dangerInk },
    // Not a site token: an orange-red told apart from the danger red.
    cut: { color: '#FFA066', onColor: '#3A1A06', container: '#3A2412', onContainer: '#FFC9A3' },
    attention: { color: site.amber, onColor: site.amberSoft, container: site.warnBg, onContainer: site.warnInk },
    neutral: { color: site.muted, onColor: site.bg, container: site.well, onContainer: site.ink },
  },
};

/** QR codes stay black on white in both modes: scanners expect it. */
export const qr = { dark: '#000000', light: '#FFFFFF' } as const;

/**
 * Headers and tab bar (#128): no more solid green block. In dark mode, the
 * site's header (.k-top): the page's own background, set apart by a thin
 * green line, the active tab in the site's green. The system bars (drawn
 * edge to edge, over the header and the tab bar) take the same --bg. In
 * light mode the same on the paper. statusBar is the style of the status
 * bar icons drawn over the header.
 */
export type Chrome = {
  background: string;
  onBackground: string;
  active: string;
  inactive: string;
  border: string;
  statusBar: 'light' | 'dark';
};
export const chrome: Record<'light' | 'dark', Chrome> = {
  light: {
    background: site.paper,
    onBackground: site.paperInk,
    active: site.brand,
    inactive: site.paperMuted,
    border: paperBand.line,
    statusBar: 'dark',
  },
  dark: {
    background: site.bg,
    onBackground: site.ink,
    active: site.green,
    inactive: site.muted,
    border: site.line,
    statusBar: 'light',
  },
};

/** The semantic colours of the current scheme. */
export function useSemantic(): Semantic {
  return semantic[useTheme().dark ? 'dark' : 'light'];
}

/** The header and tab bar colours of the current scheme. */
export function useChrome(): Chrome {
  return chrome[useTheme().dark ? 'dark' : 'light'];
}

/** WCAG 2 contrast ratio of two #RRGGBB colours, from 1 to 21. */
export function contrast(a: string, b: string): number {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

function luminance(hex: string): number {
  const n = parseInt(hex.replace('#', '').slice(0, 6), 16);
  const [r, g, b] = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((v) => {
    const c = v / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

// The site's three families (#128), bundled with the app and loaded in
// _layout.tsx (useFonts), one file per weight. Keys are the family names.
//  - Inter: the text
//  - Chakra Petch: screen titles, section headers, big numbers
//  - JetBrains Mono: the technical (MAC, IP, codes)
// On Android a custom family with fontWeight is not mapped to a heavier
// file: a weight is chosen by its family name, never with fontWeight.
export const fontFamilies = {
  body: {
    regular: 'Inter_400Regular',
    medium: 'Inter_500Medium',
    semibold: 'Inter_600SemiBold',
    bold: 'Inter_700Bold',
  },
  display: {
    medium: 'ChakraPetch_500Medium',
    semibold: 'ChakraPetch_600SemiBold',
    bold: 'ChakraPetch_700Bold',
  },
  mono: {
    medium: 'JetBrainsMono_500Medium',
    semibold: 'JetBrainsMono_600SemiBold',
  },
} as const;

/** Inter, the text family (kept under its old name). */
export const interFamilies = fontFamilies.body;

/** Ready-made text styles: the technical (MAC, IP, codes) and a heading. */
export const fontStyles = {
  mono: { fontFamily: fontFamilies.mono.medium },
  monoStrong: { fontFamily: fontFamilies.mono.semibold },
  display: { fontFamily: fontFamilies.display.semibold },
} as const;

/** The family of a Paper text variant: Chakra Petch for titles, Inter elsewhere. */
export function variantFamily(variant: string, weight: number): string {
  if (variant.startsWith('display')) return fontFamilies.display.bold;
  if (variant.startsWith('headline') || variant.startsWith('title')) return fontFamilies.display.semibold;
  // Buttons and chips: the site's buttons are bolder than MD3's 500.
  if (variant === 'labelLarge') return fontFamilies.body.semibold;
  return weight >= 500 ? fontFamilies.body.medium : fontFamilies.body.regular;
}

const paperFonts = configureFonts({
  config: Object.fromEntries(
    Object.entries(MD3LightTheme.fonts).map(([variant, f]) => [
      variant,
      {
        ...f,
        fontFamily: variantFamily(variant, Number((f as { fontWeight?: string }).fontWeight)),
        fontWeight: undefined,
      },
    ]),
  ),
});

const navFonts: NavTheme['fonts'] = {
  regular: { fontFamily: fontFamilies.body.regular, fontWeight: 'normal' },
  medium: { fontFamily: fontFamilies.body.medium, fontWeight: 'normal' },
  bold: { fontFamily: fontFamilies.body.semibold, fontWeight: 'normal' },
  heavy: { fontFamily: fontFamilies.body.bold, fontWeight: 'normal' },
};

/** Header titles: Chakra Petch, as the site's titles. */
export const headerTitleStyle = { fontFamily: fontFamilies.display.semibold } as const;

// Paper's MD3 radii are multiples of roundness: buttons 5x (10, the site's
// tall buttons), cards 3x (cards use KCard, at the site's 10).
const roundness = 2;

// Light: the site's « paper » band, for the sun outdoors.
const light: MD3Theme = {
  ...MD3LightTheme,
  roundness,
  fonts: paperFonts,
  colors: {
    ...MD3LightTheme.colors,
    primary: site.brand,
    onPrimary: '#FFFFFF',
    primaryContainer: '#D6ECDF',
    onPrimaryContainer: site.onGreen,
    secondary: '#3E4F47',
    onSecondary: '#FFFFFF',
    secondaryContainer: '#DCE8DF',
    onSecondaryContainer: site.paperInk,
    tertiary: '#7A5900',
    onTertiary: '#FFFFFF',
    tertiaryContainer: '#F6E2A8',
    onTertiaryContainer: site.warnBg,
    error: '#B3261E',
    onError: '#FFFFFF',
    errorContainer: '#F9DCD6',
    onErrorContainer: '#410E0B',
    background: site.paper,
    onBackground: site.paperInk,
    surface: site.paper,
    onSurface: site.paperInk,
    surfaceVariant: paperBand.well,
    onSurfaceVariant: site.paperMuted,
    outline: paperBand.outline,
    outlineVariant: paperBand.line,
    inverseSurface: site.paperInk,
    inverseOnSurface: site.paper,
    inversePrimary: site.green,
    surfaceDisabled: 'rgba(19,34,28,0.12)',
    onSurfaceDisabled: 'rgba(19,34,28,0.38)',
    backdrop: 'rgba(19,34,28,0.4)',
    // MD3's default elevation tints are purple (its baseline primary).
    elevation: {
      level0: 'transparent',
      level1: paperBand.card,
      level2: paperBand.cardHi,
      level3: paperBand.card,
      level4: paperBand.card,
      level5: '#FFFFFF',
    },
  },
};

// Dark: the site as it is.
const dark: MD3Theme = {
  ...MD3DarkTheme,
  roundness,
  fonts: paperFonts,
  colors: {
    ...MD3DarkTheme.colors,
    primary: site.green,
    onPrimary: site.onGreen,
    primaryContainer: site.cardHi,
    onPrimaryContainer: site.greenDark,
    secondary: site.ink2,
    onSecondary: site.onGreen,
    secondaryContainer: site.cardHi,
    onSecondaryContainer: site.ink2,
    tertiary: site.amber,
    onTertiary: site.amberSoft,
    tertiaryContainer: site.warnBg,
    onTertiaryContainer: site.warnInk,
    error: site.danger,
    onError: site.dangerBg,
    errorContainer: site.dangerBg,
    onErrorContainer: site.dangerInk,
    background: site.bg,
    onBackground: site.ink,
    surface: site.surface,
    onSurface: site.ink,
    surfaceVariant: site.well,
    onSurfaceVariant: site.muted,
    outline: site.lineStrong,
    outlineVariant: site.line,
    inverseSurface: site.ink,
    inverseOnSurface: site.bg,
    inversePrimary: site.brand,
    surfaceDisabled: 'rgba(232,242,237,0.12)',
    onSurfaceDisabled: 'rgba(232,242,237,0.38)',
    backdrop: 'rgba(5,14,11,0.6)',
    elevation: {
      level0: 'transparent',
      level1: site.card,
      level2: site.cardHi,
      level3: site.card,
      level4: site.card,
      level5: site.cardHi,
    },
  },
};

function navigationTheme(p: MD3Theme, base: NavTheme): NavTheme {
  const bars = chrome[p.dark ? 'dark' : 'light'];
  return {
    ...base,
    dark: p.dark,
    fonts: navFonts,
    colors: {
      ...base.colors,
      primary: p.colors.primary,
      background: p.colors.background,
      // Headers and tab bar: the chrome, its text and icons set from the
      // same chrome (headerTintColor, tab tints).
      card: bars.background,
      text: p.colors.onSurface,
      border: bars.border,
      notification: p.colors.error,
    },
  };
}

export function themes(scheme: 'light' | 'dark' | null | undefined) {
  const paper = scheme === 'dark' ? dark : light;
  return {
    paper,
    navigation: navigationTheme(paper, scheme === 'dark' ? NavDark : NavLight),
    chrome: chrome[scheme === 'dark' ? 'dark' : 'light'],
  };
}
