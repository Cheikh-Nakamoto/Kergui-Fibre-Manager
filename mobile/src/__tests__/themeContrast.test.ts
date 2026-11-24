// Every colour pair the app writes text or icons with stays readable outdoors
// (WCAG AA, #75): a later tweak of the palette cannot make a label vanish.
import { chrome, contrast, semantic, semanticRoles, themes } from '../ui/theme';

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

describe.each(schemes)('brand chrome (headers, tab bar), %s', (scheme) => {
  const c = chrome[scheme];
  it('titles and the active tab read on the green', () => {
    expect(contrast(c.onBackground, c.background)).toBeGreaterThanOrEqual(TEXT);
    expect(contrast(c.active, c.background)).toBeGreaterThanOrEqual(TEXT);
  });
  it('inactive tabs (label and icon) still read', () => {
    expect(contrast(c.inactive, c.background)).toBeGreaterThanOrEqual(TEXT);
  });
});

describe.each(schemes)('Paper roles, %s', (scheme) => {
  const c = themes(scheme).paper.colors;
  it('primary and its container stay readable', () => {
    expect(contrast(c.onPrimary, c.primary)).toBeGreaterThanOrEqual(TEXT);
    expect(contrast(c.onPrimaryContainer, c.primaryContainer)).toBeGreaterThanOrEqual(TEXT);
    expect(contrast(c.primary, c.background)).toBeGreaterThanOrEqual(TEXT);
  });
});
