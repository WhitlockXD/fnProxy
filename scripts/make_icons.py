"""Render the fnProxy routing icon at the sizes required by fnOS."""

from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter


ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "packaging" / "icons"
OUT.mkdir(parents=True, exist_ok=True)


def point(size: int, x: float, y: float) -> tuple[int, int]:
    return round(size * x), round(size * y)


def bezier(size: int, start: tuple[float, float], control1: tuple[float, float],
           control2: tuple[float, float], end: tuple[float, float]) -> list[tuple[int, int]]:
    result = []
    for step in range(41):
        t = step / 40
        u = 1 - t
        x = u**3 * start[0] + 3 * u**2 * t * control1[0] + 3 * u * t**2 * control2[0] + t**3 * end[0]
        y = u**3 * start[1] + 3 * u**2 * t * control1[1] + 3 * u * t**2 * control2[1] + t**3 * end[1]
        result.append(point(size, x, y))
    return result


def circle(draw: ImageDraw.ImageDraw, size: int, x: float, y: float, radius: float,
           fill: tuple[int, int, int, int]) -> None:
    cx, cy = point(size, x, y)
    r = round(size * radius)
    draw.ellipse((cx-r, cy-r, cx+r, cy+r), fill=fill)


def make(size: int, path: Path) -> None:
    canvas = size * 4
    image = Image.new("RGBA", (canvas, canvas), (0, 0, 0, 0))
    pixels = image.load()
    for y in range(canvas):
        for x in range(canvas):
            mix = min(1.0, max(0.0, 0.58 * x / canvas + 0.42 * y / canvas))
            pixels[x, y] = (
                round(24 - 13 * mix), round(111 - 63 * mix),
                round(224 - 81 * mix), 255,
            )

    glow = Image.new("RGBA", (canvas, canvas), (0, 0, 0, 0))
    glow_draw = ImageDraw.Draw(glow)
    glow_draw.ellipse((canvas * .34, -canvas * .35, canvas * 1.25, canvas * .56), fill=(69, 211, 255, 70))
    glow = glow.filter(ImageFilter.GaussianBlur(canvas * .11))
    image = Image.alpha_composite(image, glow)

    mask = Image.new("L", (canvas, canvas), 0)
    ImageDraw.Draw(mask).rounded_rectangle(
        (canvas * .035, canvas * .035, canvas * .965, canvas * .965),
        radius=canvas * .22, fill=255,
    )
    image.putalpha(mask)

    draw = ImageDraw.Draw(image)
    white = (255, 255, 255, 255)
    line_width = round(canvas * .073)
    draw.line([point(canvas, .25, .5), point(canvas, .49, .5)], fill=white, width=line_width)
    draw.line(bezier(canvas, (.49, .5), (.63, .5), (.61, .29), (.75, .29)), fill=white, width=line_width, joint="curve")
    draw.line(bezier(canvas, (.49, .5), (.63, .5), (.61, .71), (.75, .71)), fill=white, width=line_width, joint="curve")
    for x, y in ((.25, .5), (.75, .29), (.75, .71)):
        circle(draw, canvas, x, y, .075, white)
        circle(draw, canvas, x, y, .034, (57, 207, 245, 255))
    circle(draw, canvas, .49, .5, .055, white)

    image.resize((size, size), Image.Resampling.LANCZOS).save(path)


for icon_size in (64, 256):
    make(icon_size, OUT / f"icon_{icon_size}.png")

make(64, ROOT / "web" / "icon.png")
