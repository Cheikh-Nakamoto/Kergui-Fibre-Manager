// Every colour pair the app writes text or icons with stays readable outdoors
// (WCAG AA, #75): a later tweak of the palette cannot make a label vanish.
import { chrome, contrast, qr, semantic, semanticRoles, themes } from '../ui/theme';

const schemes = ['light', 'dark'] as const;
// Text: 4.5:1. Icons are held to the same bar (they sit beside the word).
const TEXT = 4.5;

describe('contrast()', () => {
  it('follows the WCAG relative luminance formula', () => {
    expect(contrast('#000000', '#FFFFFF')).toBeCloseTo(21, 5);
    expect(contrast('#FFFFFF', '#FFFFFF')).toBeCloseTo(1, 5);
    // Order does not matter.
    expect(contrast('#767676', '#FFFFFF')).toBeCloseTo(contrast('#FFFFFF', '#767676'), 10);
    expect(contrast('#767676', '#FFFFFF')).toBeGreaterThanOrEqual(4.5);
  });
});

describe.each(schemes)('semantic colours, %s', (scheme) => {
  const tones = semantic[scheme];
  const paper = themes(scheme).paper;
  // Where a role's colour is drawn straight on the app's own surfaces: the
  // screen, cards, unread rows.
  const surfaces = [paper.colors.background, paper.colors.surface, paper.colors.elevation.level1, paper.colors.elevation.level2];

  it.each(semanticRoles)('%s: the on-colour reads on the colour, the on-container on the container', (role) => {
    const tone = tones[role];
    expect(contrast(tone.onColor, tone.color)).toBeGreaterThanOrEqual(TEXT);
    expect(contrast(tone.onContainer, tone.container)).toBeGreaterThanOrEqual(TEXT);
  });

  // Outlined buttons (« Couper Internet ») write in the role's colour on the
  // surface. Amber is never written as text: it only fills badges and chips.
  it.each(semanticRoles.filter((r) => r !== 'attention'))('%s: its colour reads as text on the surfaces', (role) => {
    for (const s of surfaces) expect(contrast(tones[role].color, s)).toBeGreaterThanOrEqual(TEXT);
  });

  it('keeps red for blocking: the roles are told apart', () => {
    expect(tones.blocked.color).not.toBe(tones.cut.color);
    expect(tones.blocked.color).not.toBe(tones.connected.color);
    expect(tones.attention.color).not.toBe(tones.cut.color);
  });
});

describe.each(schemes)('chrome (headers, tab bar), %s', (scheme) => {
  const c = chrome[scheme];
  it('titles and the active tab read on the chrome', () => {
    expect(contrast(c.onBackground, c.background)).toBeGreaterThanOrEqual(TEXT);
    expect(contrast(c.active, c.background)).toBeGreaterThanOrEqual(TEXT);
  });
  it('inactive tabs (label and icon) still read', () => {
    expect(contrast(c.inactive, c.background)).toBeGreaterThanOrEqual(TEXT);
  });
  it('the status bar icons suit the chrome under them', () => {
    expect(c.statusBar).toBe(contrast('#FFFFFF', c.background) > contrast('#000000', c.background) ? 'light' : 'dark');
  });
});

describe.each(schemes)('Paper roles, %s', (scheme) => {
  const c = themes(scheme).paper.colors;
  // The screen, the surface, cards (level1), unread rows and green cards
  // (level2), dialogs (level3).
  const surfaces = [c.background, c.surface, c.elevation.level1, c.elevation.level2, c.elevation.level3];

  it('primary and its container stay readable', () => {
    expect(contrast(c.onPrimary, c.primary)).toBeGreaterThanOrEqual(TEXT);
    expect(contrast(c.onPrimaryContainer, c.primaryContainer)).toBeGreaterThanOrEqual(TEXT);
    expect(contrast(c.primary, c.background)).toBeGreaterThanOrEqual(TEXT);
  });

  // #128: the site's (or the paper band's) text on every surface.
  it('text and secondary text read on every surface', () => {
    for (const s of surfaces) {
      expect(contrast(c.onSurface, s)).toBeGreaterThanOrEqual(TEXT);
      expect(contrast(c.onBackground, s)).toBeGreaterThanOrEqual(TEXT);
      expect(contrast(c.onSurfaceVariant, s)).toBeGreaterThanOrEqual(TEXT);
      // Text buttons and links write in primary; the journal writes errors
      // and warnings in error / tertiary.
      expect(contrast(c.primary, s)).toBeGreaterThanOrEqual(TEXT);
      expect(contrast(c.error, s)).toBeGreaterThanOrEqual(TEXT);
      expect(contrast(c.tertiary, s)).toBeGreaterThanOrEqual(TEXT);
      expect(contrast(c.secondary, s)).toBeGreaterThanOrEqual(TEXT);
    }
  });

  it('the wells (inputs, tracks, neutral badges) keep their text', () => {
    expect(contrast(c.onSurface, c.surfaceVariant)).toBeGreaterThanOrEqual(TEXT);
    expect(contrast(c.onSurfaceVariant, c.surfaceVariant)).toBeGreaterThanOrEqual(TEXT);
  });

  it('the containers keep their text: tonal buttons, chips, warnings, errors', () => {
    expect(contrast(c.onSecondary, c.secondary)).toBeGreaterThanOrEqual(TEXT);
    expect(contrast(c.onSecondaryContainer, c.secondaryContainer)).toBeGreaterThanOrEqual(TEXT);
    expect(contrast(c.onTertiary, c.tertiary)).toBeGreaterThanOrEqual(TEXT);
    expect(contrast(c.onTertiaryContainer, c.tertiaryContainer)).toBeGreaterThanOrEqual(TEXT);
    expect(contrast(c.onError, c.error)).toBeGreaterThanOrEqual(TEXT);
    expect(contrast(c.onErrorContainer, c.errorContainer)).toBeGreaterThanOrEqual(TEXT);
    // Snackbars.
    expect(contrast(c.inverseOnSurface, c.inverseSurface)).toBeGreaterThanOrEqual(TEXT);
  });
});

// In the sun, an input's border is the field itself (WCAG 1.4.11, 3:1). The
// dark mode keeps the site's translucent line-strong (about 2.3:1 on its
// background): there the label and the green focus mark the field.
describe('light mode: inputs and outlined buttons are bordered at 3:1', () => {
  const c = themes('light').paper.colors;
  it.each([c.background, c.elevation.level1, c.elevation.level2])('on %s', (s) => {
    expect(contrast(c.outline, s)).toBeGreaterThanOrEqual(3);
  });
});

describe('QR codes', () => {
  it('stay black on white', () => {
    expect(contrast(qr.dark, qr.light)).toBeCloseTo(21, 5);
  });
});
