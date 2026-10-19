"""Draws the Kergui logo ("kër gui": the house) and writes every app icon.

Glyph: a roof over three Wi-Fi arcs and a dot. Drawn at 4x and downsampled
for clean edges. Usage: python3 scripts/mobile/make_icons.py mobile/assets  (needs Pillow)
"""
import math
import sys

from PIL import Image, ImageDraw

GREEN = (11, 110, 79)        # #0B6E4F brand primary
GREEN_DARK = (6, 74, 53)     # gradient end
AMBER = (242, 169, 0)        # #F2A900 accent
WHITE = (255, 255, 255)
SS = 4                        # supersampling


def glyph(size, fg=WHITE, dot=AMBER, scale=1.0):
    """Transparent square image with the glyph centred, scaled to `scale`."""
    S = size * SS
    img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    u = S * scale  # glyph unit box
    ox = oy = (S - u) / 2

    def p(x, y):
        return (ox + x * u, oy + y * u)

    w = 0.075 * u
    # Roof: chevron with round joints.
    pts = [p(0.16, 0.50), p(0.50, 0.18), p(0.84, 0.50)]
    d.line(pts, fill=fg, width=int(w), joint="curve")
    for q in (pts[0], pts[2]):
        d.ellipse([q[0] - w / 2, q[1] - w / 2, q[0] + w / 2, q[1] + w / 2], fill=fg)

    # Wi-Fi arcs, opening upwards, centred under the roof.
    cx, cy = p(0.50, 0.86)
    aw = 0.07 * u
    for r in (0.30, 0.19):
        R = r * u
        box = [cx - R, cy - R, cx + R, cy + R]
        d.arc(box, start=225, end=315, fill=fg, width=int(aw))
        for ang in (225, 315):  # round caps
            a = math.radians(ang)
            ex, ey = cx + (R - aw / 2) * math.cos(a), cy + (R - aw / 2) * math.sin(a)
            d.ellipse([ex - aw / 2, ey - aw / 2, ex + aw / 2, ey + aw / 2], fill=fg)
    R = 0.06 * u
    d.ellipse([cx - R, cy - R - 0.02 * u, cx + R, cy + R - 0.02 * u], fill=dot)
    return img.resize((size, size), Image.LANCZOS)


def gradient(size):
    img = Image.new("RGB", (size, size))
    d = ImageDraw.Draw(img)
    for y in range(size):
        t = y / (size - 1)
        c = tuple(int(a + (b - a) * t) for a, b in zip(GREEN, GREEN_DARK))
        d.line([(0, y), (size, y)], fill=c)
    return img


def main(out):
    # iOS / generic icon: full-bleed, opaque (iOS masks the corners itself).
    icon = gradient(1024).convert("RGBA")
    icon.alpha_composite(glyph(1024, scale=0.72))
    icon.convert("RGB").save(f"{out}/icon.png", optimize=True)

    # Android adaptive icon: glyph inside the 66% safe zone, separate layers.
    glyph(1024, scale=0.50).save(f"{out}/android-icon-foreground.png", optimize=True)
    gradient(1024).save(f"{out}/android-icon-background.png", optimize=True)
    glyph(1024, fg=WHITE, dot=WHITE, scale=0.50).save(f"{out}/android-icon-monochrome.png", optimize=True)

    # Splash / in-app logo: green glyph on transparent.
    glyph(1024, fg=GREEN, scale=0.9).save(f"{out}/splash-icon.png", optimize=True)
    glyph(512, fg=GREEN, scale=0.9).save(f"{out}/logo.png", optimize=True)

    fav = gradient(48).convert("RGBA")
    fav.alpha_composite(glyph(48, scale=0.8))
    fav.convert("RGB").save(f"{out}/favicon.png", optimize=True)


if __name__ == "__main__":
    main(sys.argv[1])
