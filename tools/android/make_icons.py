#!/usr/bin/env python3
"""Draws the launcher icon (res/mipmap-*/ic_launcher.png): a 4x4 pad grid in the installer page's colours, one pad lit.
Run it again after changing the design; the PNGs are committed so the APK build needs no Python."""
import os
from PIL import Image, ImageDraw

HERE = os.path.dirname(os.path.abspath(__file__))
BG, PAD, LIT = (27, 28, 31), (233, 231, 226), (214, 58, 48)  # --fg, --bg, --accent of tools/desktop/web/index.html
SIZES = {"mdpi": 48, "hdpi": 72, "xhdpi": 96, "xxhdpi": 144, "xxxhdpi": 192}


def icon(px):
    s = 8  # draw large, scale down for smooth edges
    n = px * s
    im = Image.new("RGBA", (n, n), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    m = n * 0.06
    d.rounded_rectangle([m, m, n - m, n - m], radius=n * 0.2, fill=BG)
    inner, gap = n * 0.2, n * 0.035
    cell = (n - 2 * inner - 3 * gap) / 4
    for r in range(4):
        for c in range(4):
            x, y = inner + c * (cell + gap), inner + r * (cell + gap)
            d.rounded_rectangle([x, y, x + cell, y + cell], radius=cell * 0.18, fill=LIT if (r, c) == (1, 2) else PAD)
    return im.resize((px, px), Image.LANCZOS)


if __name__ == "__main__":
    for name, px in SIZES.items():
        out = os.path.join(HERE, "res", "mipmap-" + name)
        os.makedirs(out, exist_ok=True)
        icon(px).save(os.path.join(out, "ic_launcher.png"), optimize=True)
