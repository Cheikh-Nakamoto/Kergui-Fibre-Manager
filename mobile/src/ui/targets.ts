// Touch targets (#125, audit M7): 48 × 48 dp, 8 dp apart (Android's
// accessibility help; WCAG 2.2 SC 2.5.8 sets 24 × 24 as the floor). Paper
// draws its controls smaller: a hitSlop grows the touch area without moving
// anything on screen.

/** Android's touch target, in dp. */
export const TARGET = 48;

/** The room left between two targets, in dp. */
export const TARGET_GAP = 8;

type Slop = { top: number; bottom: number; left: number; right: number };

/** The hitSlop that grows a `width` × `height` control to TARGET × TARGET. */
export function slopFor(width: number, height: number): Slop {
  const x = Math.max(0, Math.ceil((TARGET - width) / 2));
  const y = Math.max(0, Math.ceil((TARGET - height) / 2));
  return { top: y, bottom: y, left: x, right: x };
}

/** Paper's IconButton at its default size: a 24 dp icon in 40 dp. */
export const iconButtonSlop = slopFor(40, 40);

/** Paper's MD3 Button: 40 dp high, at least 64 dp wide. */
export const buttonSlop = slopFor(64, 40);

/** Paper's MD3 Chip: 32 dp high. */
export const chipSlop = slopFor(48, 32);
