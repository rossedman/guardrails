"""Generate the README header and social preview images.

Usage: python3 scripts/header.py   (requires: pip install cairosvg)
Writes docs/assets/header.svg, docs/assets/header.png and
docs/assets/social-preview.png. Fonts: Inter and DejaVu Sans Mono.
"""

import html
import pathlib

import cairosvg

OUT = pathlib.Path(__file__).resolve().parent.parent / "docs" / "assets"

BG0, BG1 = "#0A0F1E", "#131C38"
AMBER, AMBER_DIM = "#F5B83D", "#B9852A"
GREEN, RED, BLUE = "#4ADE80", "#F87171", "#7DD3FC"
TEXT, MUTED, FAINT = "#F1F5F9", "#94A3B8", "#475569"
SANS = "Inter, 'Inter Display', sans-serif"
MONO = "'DejaVu Sans Mono', monospace"


def shield(x, y, s):
    """A shield with a check mark, top-left at (x, y), size s."""
    w, h = s, s * 1.18
    path = (
        f"M {x + w / 2} {y} "
        f"L {x + w} {y + h * 0.17} "
        f"L {x + w} {y + h * 0.5} "
        f"C {x + w} {y + h * 0.78} {x + w * 0.72} {y + h * 0.93} {x + w / 2} {y + h} "
        f"C {x + w * 0.28} {y + h * 0.93} {x} {y + h * 0.78} {x} {y + h * 0.5} "
        f"L {x} {y + h * 0.17} Z"
    )
    check = (
        f"M {x + w * 0.29} {y + h * 0.52} "
        f"L {x + w * 0.45} {y + h * 0.67} "
        f"L {x + w * 0.73} {y + h * 0.37}"
    )
    return (
        f'<path d="{path}" fill="url(#shield)" stroke="{AMBER}" stroke-width="3"/>'
        f'<path d="{check}" fill="none" stroke="{BG0}" stroke-width="{s * 0.09}" '
        f'stroke-linecap="round" stroke-linejoin="round"/>'
    )


def guardrail(y, width, offset=0):
    """A W-beam highway guardrail with posts along the bottom edge."""
    parts = []
    for px in range(offset, width + 80, 80):
        parts.append(
            f'<rect x="{px}" y="{y - 6}" width="8" height="70" rx="2" fill="{FAINT}" opacity="0.55"/>'
        )
    reflectors = "".join(
        f'<rect x="{px - 3}" y="{y + 5}" width="14" height="12" rx="2" fill="{AMBER}" opacity="0.9"/>'
        for px in range(offset, width + 80, 160)
    )
    parts.append(
        f'<rect x="0" y="{y}" width="{width}" height="22" rx="4" fill="url(#rail)"/>'
        f'<rect x="0" y="{y + 9}" width="{width}" height="3" fill="{BG0}" opacity="0.35"/>'
        + reflectors
    )
    return "".join(parts)


def terminal(x, y, w):
    rows = [
        ("PostToolUse", [("gofumpt", GREEN, "✓"), ("golangci-lint", GREEN, "✓")], "0.8s"),
        ("PreToolUse", [("//nolint", RED, "denied")], "4ms"),
        ("PreToolUse", [("terraform apply", RED, "denied")], "3ms"),
        ("Stop", [("tests", GREEN, "✓"), ("coverage 84.2% < 90%", AMBER, "block")], "41s"),
        ("Stop", [("tests", GREEN, "✓"), ("coverage 92.6%", GREEN, "✓"), ("lint", GREEN, "✓")], "38s"),
    ]
    row_h, pad = 46, 30
    h = 48 + pad + row_h * len(rows) + 4
    out = [
        f'<g filter="url(#shadow)"><rect x="{x}" y="{y}" width="{w}" height="{h}" rx="16" fill="#0D1428" stroke="#24304F" stroke-width="1.5"/></g>',
        f'<rect x="{x}" y="{y}" width="{w}" height="48" rx="16" fill="#151E3A"/>',
        f'<rect x="{x}" y="{y + 32}" width="{w}" height="16" fill="#151E3A"/>',
    ]
    for i, c in enumerate(["#FF5F57", "#FEBC2E", "#28C840"]):
        out.append(f'<circle cx="{x + 26 + i * 22}" cy="{y + 24}" r="6.5" fill="{c}"/>')
    out.append(
        f'<text x="{x + w / 2}" y="{y + 30}" font-family="{MONO}" font-size="15" fill="{MUTED}" text-anchor="middle">claude · guardrails</text>'
    )
    ty = y + 48 + pad + 14
    for event, items, took in rows:
        out.append(f'<circle cx="{x + 30}" cy="{ty - 6}" r="5" fill="{BLUE}"/>')
        out.append(f'<text x="{x + 46}" y="{ty}" font-family="{MONO}" font-size="18" fill="{BLUE}">{event}</text>')
        cx = x + 200
        for label, color, mark in items:
            text = f"{label} {mark}"
            out.append(f'<text x="{cx}" y="{ty}" font-family="{MONO}" font-size="18" fill="{color}">{html.escape(text)}</text>')
            cx += len(text) * 11 + 24
        out.append(
            f'<text x="{x + w - 24}" y="{ty}" font-family="{MONO}" font-size="16" fill="{FAINT}" text-anchor="end">{took}</text>'
        )
        ty += row_h
    return "".join(out)


def svg(width, height, social=False):
    scale = height / 600
    term_w = 690
    term_x = width - term_w - 70
    langs = ["Go", "Python", "Terraform", "Docker", "YAML", "Shell"]
    pills, px = [], 96
    for i, lang in enumerate(langs):
        pw = len(lang) * 11 + 34
        live = i == 0
        pills.append(
            f'<rect x="{px}" y="{392}" width="{pw}" height="34" rx="17" '
            f'fill="{AMBER if live else "none"}" fill-opacity="{0.14 if live else 0}" '
            f'stroke="{AMBER if live else FAINT}" stroke-width="1.5"/>'
            f'<text x="{px + pw / 2}" y="{415}" font-family="{SANS}" font-size="17" font-weight="600" '
            f'fill="{AMBER if live else MUTED}" text-anchor="middle">{lang}</text>'
        )
        px += pw + 10
    grid = "".join(
        f'<line x1="{gx}" y1="0" x2="{gx}" y2="{height}" stroke="#1B2547" stroke-width="1"/>'
        for gx in range(0, width, 40)
    ) + "".join(
        f'<line x1="0" y1="{gy}" x2="{width}" y2="{gy}" stroke="#1B2547" stroke-width="1"/>'
        for gy in range(0, height, 40)
    )
    tagline_2 = "Format on every edit. Gate every turn."
    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">
  <defs>
    <linearGradient id="bg" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0" stop-color="{BG0}"/><stop offset="1" stop-color="{BG1}"/>
    </linearGradient>
    <radialGradient id="glow" cx="0.18" cy="0.35" r="0.55">
      <stop offset="0" stop-color="{AMBER}" stop-opacity="0.16"/><stop offset="1" stop-color="{AMBER}" stop-opacity="0"/>
    </radialGradient>
    <radialGradient id="glow2" cx="0.8" cy="0.4" r="0.5">
      <stop offset="0" stop-color="{BLUE}" stop-opacity="0.10"/><stop offset="1" stop-color="{BLUE}" stop-opacity="0"/>
    </radialGradient>
    <linearGradient id="shield" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#FFD27A"/><stop offset="1" stop-color="{AMBER}"/>
    </linearGradient>
    <linearGradient id="rail" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#CBD5E1"/><stop offset="0.5" stop-color="#64748B"/><stop offset="1" stop-color="#334155"/>
    </linearGradient>
    <linearGradient id="fade" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="{BG0}" stop-opacity="0"/><stop offset="1" stop-color="{BG0}" stop-opacity="1"/>
    </linearGradient>
    <filter id="shadow" x="-10%" y="-10%" width="120%" height="130%">
      <feDropShadow dx="0" dy="18" stdDeviation="22" flood-color="#000" flood-opacity="0.55"/>
    </filter>
  </defs>
  <rect width="{width}" height="{height}" fill="url(#bg)"/>
  <g opacity="0.55">{grid}</g>
  <rect width="{width}" height="{height}" fill="url(#glow)"/>
  <rect width="{width}" height="{height}" fill="url(#glow2)"/>
  <g transform="scale({scale})">
    {shield(96, 120, 64)}
    <text x="182" y="178" font-family="{SANS}" font-size="78" font-weight="800" fill="{TEXT}" letter-spacing="-2">guardrails</text>
    <text x="96" y="268" font-family="{SANS}" font-size="34" font-weight="600" fill="{TEXT}">Quality gates for <tspan fill="{AMBER}">Claude Code</tspan></text>
    <text x="96" y="318" font-family="{SANS}" font-size="23" fill="{MUTED}">{tagline_2}</text>
    <text x="96" y="352" font-family="{SANS}" font-size="23" fill="{MUTED}">Claude can&#8217;t move the goalposts.</text>
    {"".join(pills)}
    {terminal(term_x / scale if social else term_x, 110, term_w)}
    {guardrail(520, int(width / scale) + 80, 40)}
    <rect x="0" y="560" width="{int(width / scale) + 80}" height="40" fill="url(#fade)"/>
  </g>
</svg>
"""


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    header = svg(1600, 600)
    (OUT / "header.svg").write_text(header)
    cairosvg.svg2png(bytestring=header.encode(), write_to=str(OUT / "header.png"))
    social = svg(1600, 600, social=True)
    cairosvg.svg2png(bytestring=social.encode(), write_to=str(OUT / "social-preview.png"),
                     output_width=1280, output_height=480)
    print("wrote", ", ".join(p.name for p in sorted(OUT.glob("*"))))


if __name__ == "__main__":
    main()
