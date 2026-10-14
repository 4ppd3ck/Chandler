#!/usr/bin/env python3
"""Generate the chandler app icon set.

Deterministic and Pillow-only. Draws the master 1024x1024 icon (a
rounded-square gradient with a bold "C" monogram) and derives every
platform asset from it, so all shipped icons stay pixel-consistent:

    icons/chandler-1024.png    master, RGBA
    icons/chandler.ico         Windows, sizes 16..256
    icons/chandler.icns        macOS,   sizes 16..1024
    icons/png/<N>x<N>.png      Linux hicolor set, 16..512

Run from the repository root:

    python icons/generate_icons.py
"""

import os
import sys

from PIL import Image, ImageDraw, ImageFont

HERE = os.path.dirname(os.path.abspath(__file__))
MASTER = 1024
SS = 4  # supersample factor for the rounded-corner mask

ICO_SIZES = [16, 24, 32, 48, 64, 128, 256]
ICNS_SIZES = [16, 32, 64, 128, 256, 512]
PNG_SIZES = [16, 24, 32, 48, 64, 128, 256, 512]

TOP = (124, 92, 252)  # violet
BOTTOM = (58, 108, 247)  # blue
INK = (255, 255, 255, 255)


def find_font(px):
    """Return a bold TrueType font at px, or Pillow's scalable default."""
    windir = os.environ.get("WINDIR", r"C:\Windows")
    candidates = [
        os.path.join(windir, "Fonts", "arialbd.ttf"),
        os.path.join(windir, "Fonts", "segoeuib.ttf"),
        "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
        "/usr/share/fonts/truetype/liberation/LiberationSans-Bold.ttf",
        "/System/Library/Fonts/Supplemental/Arial Bold.ttf",
        "arialbd.ttf",
        "DejaVuSans-Bold.ttf",
    ]
    for path in candidates:
        try:
            return ImageFont.truetype(path, px)
        except Exception:
            continue
    try:
        return ImageFont.load_default(size=px)
    except Exception:
        return ImageFont.load_default()


def rounded_mask(size, radius, ss=SS):
    """Antialiased rounded-square mask: draw at ss x, downscale."""
    big = Image.new("L", (size * ss, size * ss), 0)
    ImageDraw.Draw(big).rounded_rectangle(
        [0, 0, size * ss - 1, size * ss - 1], radius=radius * ss, fill=255
    )
    return big.resize((size, size), Image.LANCZOS)


def build_master():
    img = Image.new("RGBA", (MASTER, MASTER), (0, 0, 0, 0))

    # Vertical gradient, one row per scanline.
    grad = Image.new("RGBA", (1, MASTER))
    for y in range(MASTER):
        t = y / (MASTER - 1)
        grad.putpixel((0, y), (
            round(TOP[0] + (BOTTOM[0] - TOP[0]) * t),
            round(TOP[1] + (BOTTOM[1] - TOP[1]) * t),
            round(TOP[2] + (BOTTOM[2] - TOP[2]) * t),
            255,
        ))
    grad = grad.resize((MASTER, MASTER), Image.NEAREST)

    mask = rounded_mask(MASTER, int(MASTER * 0.225))
    img.paste(grad, (0, 0), mask)

    # Soft sheen in the upper-left, clipped to the rounded square.
    sheen = Image.new("RGBA", (MASTER, MASTER), (0, 0, 0, 0))
    ImageDraw.Draw(sheen).ellipse(
        [-MASTER * 0.30, -MASTER * 0.80, MASTER * 0.95, MASTER * 0.50],
        fill=(255, 255, 255, 38),
    )
    img = Image.alpha_composite(img, Image.composite(
        sheen, Image.new("RGBA", (MASTER, MASTER), (0, 0, 0, 0)), mask))

    # Bold "C" monogram, centred, with a soft offset shadow.
    font = find_font(int(MASTER * 0.66))
    layer = Image.new("RGBA", (MASTER, MASTER), (0, 0, 0, 0))
    d = ImageDraw.Draw(layer)
    box = d.textbbox((0, 0), "C", font=font)
    cx = (MASTER - (box[2] - box[0])) / 2 - box[0]
    cy = (MASTER - (box[3] - box[1])) / 2 - box[1]
    d.text((cx, cy + MASTER * 0.012), "C", font=font, fill=(20, 20, 60, 90))
    d.text((cx, cy), "C", font=font, fill=INK)
    img = Image.alpha_composite(img, layer)
    return img


def main():
    master = build_master()
    master.save(os.path.join(HERE, "chandler-1024.png"))

    master.save(os.path.join(HERE, "chandler.ico"), format="ICO",
                sizes=[(s, s) for s in ICO_SIZES])

    icns_imgs = [master.resize((s, s), Image.LANCZOS)
                 for s in reversed(ICNS_SIZES)]
    master.save(os.path.join(HERE, "chandler.icns"), format="ICNS",
                append_images=icns_imgs)

    png_dir = os.path.join(HERE, "png")
    os.makedirs(png_dir, exist_ok=True)
    for s in PNG_SIZES:
        master.resize((s, s), Image.LANCZOS).save(
            os.path.join(png_dir, "%dx%d.png" % (s, s)))

    print("wrote icons under %s" % HERE)
    for name in ("chandler-1024.png", "chandler.ico", "chandler.icns"):
        p = os.path.join(HERE, name)
        print("  %-20s %d bytes" % (name, os.path.getsize(p)))
    print("  png/                 %d sizes" % len(PNG_SIZES))


if __name__ == "__main__":
    sys.exit(main())
