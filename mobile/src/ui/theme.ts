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
import { configureFonts, MD3DarkTheme, MD3LightTheme, type MD3Theme } from 'react-native-paper';

export const brand = {
  green: '#0B6E4F', // primary, from the logo
  greenLight: '#6FD8AE', // primary on dark backgrounds
  amber: '#F2A900', // accent dot of the logo
};

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
    primaryContainer: '#C5EEDB',
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
  },
};

const dark: MD3Theme = {
  ...MD3DarkTheme,
  fonts: paperFonts,
  colors: {
    ...MD3DarkTheme.colors,
    primary: brand.greenLight,
    onPrimary: '#003826',
    primaryContainer: '#005139',
    onPrimaryContainer: '#C5EEDB',
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
  },
};

function navigationTheme(p: MD3Theme, base: NavTheme): NavTheme {
  return {
    ...base,
    dark: p.dark,
    fonts: navFonts,
    colors: {
      ...base.colors,
      primary: p.colors.primary,
      background: p.colors.background,
      card: p.colors.elevation.level2,
      text: p.colors.onSurface,
      border: p.colors.outlineVariant,
      notification: p.colors.error,
    },
  };
}

export function themes(scheme: 'light' | 'dark' | null | undefined) {
  const paper = scheme === 'dark' ? dark : light;
  return { paper, navigation: navigationTheme(paper, scheme === 'dark' ? NavDark : NavLight) };
}
