// One theme for the whole app. Paper (buttons, cards, lists) and the router's
// navigation (headers, tab bar, screen background) used to pick their light /
// dark mode separately: with the phone in dark mode, Paper drew light text and
// dark cards while the navigation kept white screens, so most text was
// unreadable. Both now come from the same scheme and the same colours.
//
// The font is Inter, bundled with the app: React Native otherwise inherits the
// phone's system font, and some phones (Samsung "font style") ship decorative
// ones.
import { DarkTheme as NavDark, DefaultTheme as NavLight, type Theme as NavTheme } from 'expo-router';
import { configureFonts, MD3DarkTheme, MD3LightTheme, useTheme, type MD3Theme } from 'react-native-paper';

// The logo's green and amber, more saturated than at first (#75: « l'app
// manque de couleur vive »).
export const brand = {
  green: '#007A4F', // primary, from the logo
  greenDeep: '#006C46', // headers and tab bar: white and the inactive tabs read on it
  greenLight: '#5DD9A0', // primary on dark backgrounds
  amber: '#FFB300', // accent dot of the logo
};

// Semantic colours (#75). Screens never pick a raw colour: they ask for the
// meaning, and the meaning carries an icon and a word next to it.
//  - connected: on the network, legitimately (green)
//  - restore: gives access back, safe and free (green)
//  - blocked: blocked by the filter, and every action that blocks (red)
//  - cut: cut off the Wi-Fi, told apart from a block (orange-red)
//  - attention: « look at this »: new, not approved, pending (amber)
// color/onColor fill small areas (badges, chips, buttons); container/
// onContainer tint cards and banners, so screens stay calm. Every pair is
// held to WCAG AA by themeContrast.test.ts.
export const semanticRoles = ['connected', 'restore', 'blocked', 'cut', 'attention'] as const;
export type SemanticRole = (typeof semanticRoles)[number];
export type Tone = { color: string; onColor: string; container: string; onContainer: string };
export type Semantic = Record<SemanticRole, Tone>;

const greenLight: Tone = { color: brand.green, onColor: '#FFFFFF', container: '#C8F5DE', onContainer: '#00391F' };
const greenDark: Tone = { color: brand.greenLight, onColor: '#003920', container: '#005234', onContainer: '#B5F2D2' };

export const semantic: Record<'light' | 'dark', Semantic> = {
  light: {
    connected: greenLight,
    restore: greenLight,
    blocked: { color: '#C62828', onColor: '#FFFFFF', container: '#FFDAD6', onContainer: '#410002' },
    cut: { color: '#B3400B', onColor: '#FFFFFF', container: '#FFDBCB', onContainer: '#3A0B00' },
    attention: { color: brand.amber, onColor: '#261A00', container: '#FFE08C', onContainer: '#261A00' },
  },
  // Lighter, softer tones on dark backgrounds: the same meanings, nothing glaring.
  dark: {
    connected: greenDark,
    restore: greenDark,
    blocked: { color: '#FFB4AB', onColor: '#690005', container: '#93000A', onContainer: '#FFDAD6' },
    cut: { color: '#FFB59A', onColor: '#5A1A00', container: '#7A2900', onContainer: '#FFDBCB' },
    attention: { color: '#FFC93C', onColor: '#3F2E00', container: '#5C4300', onContainer: '#FFE08C' },
  },
};

/** Headers and tab bar: the brand green (#75). */
export type Chrome = { background: string; onBackground: string; active: string; inactive: string };
export const chrome: Record<'light' | 'dark', Chrome> = {
  light: { background: brand.greenDeep, onBackground: '#FFFFFF', active: '#FFFFFF', inactive: '#B5E6CE' },
  dark: { background: '#123629', onBackground: '#E3F5EC', active: brand.greenLight, inactive: '#9DC2B1' },
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

// Inter weights loaded in _layout.tsx (useFonts). Keys are the family names.
export const interFamilies = {
  regular: 'Inter_400Regular',
  medium: 'Inter_500Medium',
  semibold: 'Inter_600SemiBold',
  bold: 'Inter_700Bold',
} as const;

// MD3 variants only use weights 400 and 500. On Android a custom family with
// fontWeight is not mapped to a heavier file, so each variant gets the file of
// its weight and no fontWeight.
const paperFonts = configureFonts({
  config: Object.fromEntries(
    Object.entries(MD3LightTheme.fonts).map(([variant, f]) => [
      variant,
      {
        ...f,
        fontFamily: Number((f as { fontWeight?: string }).fontWeight) >= 500 ? interFamilies.medium : interFamilies.regular,
        fontWeight: undefined,
      },
    ]),
  ),
});

const navFonts: NavTheme['fonts'] = {
  regular: { fontFamily: interFamilies.regular, fontWeight: 'normal' },
  medium: { fontFamily: interFamilies.medium, fontWeight: 'normal' },
  bold: { fontFamily: interFamilies.semibold, fontWeight: 'normal' },
  heavy: { fontFamily: interFamilies.bold, fontWeight: 'normal' },
};

const light: MD3Theme = {
  ...MD3LightTheme,
  fonts: paperFonts,
  colors: {
    ...MD3LightTheme.colors,
    primary: brand.green,
    onPrimary: '#FFFFFF',
    primaryContainer: '#C8F5DE',
    onPrimaryContainer: '#002116',
    secondary: '#4D6358',
    secondaryContainer: '#CFE9DA',
    onSecondaryContainer: '#0A1F16',
    tertiary: '#7A5900',
    tertiaryContainer: '#FFDEA0',
    background: '#F6FAF7',
    surface: '#F6FAF7',
    surfaceVariant: '#DCE5DE',
    onSurfaceVariant: '#404943',
    outline: '#707973',
    outlineVariant: '#C0C9C1',
    // MD3's default elevation tints are purple (its baseline primary).
    elevation: {
      level0: 'transparent',
      level1: '#EEF4F0',
      level2: '#E8F0EB',
      level3: '#E2ECE6',
      level4: '#E0EAE4',
      level5: '#DCE7E1',
    },
  },
};

const dark: MD3Theme = {
  ...MD3DarkTheme,
  fonts: paperFonts,
  colors: {
    ...MD3DarkTheme.colors,
    primary: brand.greenLight,
    onPrimary: '#003920',
    primaryContainer: '#005234',
    onPrimaryContainer: '#B5F2D2',
    secondary: '#B3CCBE',
    secondaryContainer: '#354B40',
    onSecondaryContainer: '#CFE9DA',
    tertiary: '#F8BD2A',
    tertiaryContainer: '#5C4200',
    background: '#0F1512',
    surface: '#0F1512',
    surfaceVariant: '#404943',
    onSurfaceVariant: '#C0C9C1',
    outline: '#8A938C',
    outlineVariant: '#404943',
    elevation: {
      level0: 'transparent',
      level1: '#151D19',
      level2: '#19221E',
      level3: '#1D2722',
      level4: '#1F2924',
      level5: '#222D27',
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
      // Headers and tab bar carry the brand green; their text and icons are
      // set from the same chrome (headerTintColor, tab tints).
      card: bars.background,
      text: p.colors.onSurface,
      border: bars.background,
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
