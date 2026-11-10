#!/usr/bin/env python3
"""Compose the brand sheet, social card, README banner, launch graphics and
architecture diagram from the hand-drawn sources.

    python3 brand/scripts/build_sheets.py            # write all composed SVGs
    python3 brand/scripts/build_sheets.py --check    # only report text contrast

The mark, wordmark and icons are hand-drawn (brand/logo, brand/icons); this
script only arranges them. All text is converted to glyph outlines by
outline_text.py, so the output has no <text> and needs no fonts. Every text
run is checked against the background it sits on (WCAG 2, 4.5:1) and the
script exits non-zero if any run fails. Colours come from palette.py.
"""
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
sys.path.insert(0, HERE)
import palette as P  # noqa: E402
from outline_text import Typesetter, single_path  # noqa: E402

WM = ("M0 24 7.5 0 12.5 0 20 24 15.81 24 13.81 17.6 6.19 17.6 4.19 24ZM10 5.41 7.32 14 12.68 14ZM21.5 0 33.5 0 "
      "33.5 3.5 29.5 3.5 29.5 20.5 33.5 20.5 33.5 24 21.5 24 21.5 20.5 25.5 20.5 25.5 3.5 21.5 3.5ZM37.1 0H41.1V24H37.1Z"
      "M41.1 0H46.6A7.5 7.5 0 0 1 46.6 15H41.1V11.25H46.6A3.75 3.75 0 0 0 46.6 3.75H41.1ZM56.5 -1.5H60.5V24H56.5Z"
      "M79.01 20.98A8.9 8.9 0 1 1 80.71 13.65L76.79 14.48A4.9 4.9 0 1 0 75.86 18.52ZM66.6 13.65H80.71V17.15H66.6Z"
      "M82.7 7 87.49 7 98.7 24 93.91 24ZM93.91 7 98.7 7 87.49 24 82.7 24Z")
LANES = "M2 6h4l10 10M2 26h4l10-10M2 16h14M24 16h6"
N = P.NEUTRAL
INK, GATE = P.BRAND["ink"]["light"], P.BRAND["gate"]["light"]
INKD, GATED = P.BRAND["ink"]["dark"], P.BRAND["gate"]["dark"]
MIST, MISTD = P.BRAND["mist"]["light"], P.BRAND["mist"]["dark"]

THEMES = {
    "light": dict(bg="#FFFFFF", panel=N["50"], line=N["200"], grid=N["100"], grid2=N["300"], text=INK, muted=N["600"],
                  accent=GATE, lane=INK, gate=GATE, mist=MIST, ok=P.STATUS["ok"]["light"]["fg"],
                  denied=P.STATUS["denied"]["light"]["fg"], mode="light"),
    "dark": dict(bg=N["950"], panel=N["900"], line=N["700"], grid=N["850"], grid2=N["700"], text=INKD, muted=N["400"],
                 accent=GATED, lane=INKD, gate=GATED, mist=MISTD, ok=P.STATUS["ok"]["dark"]["fg"],
                 denied=P.STATUS["denied"]["dark"]["fg"], mode="dark"),
}

ICON_NAMES = ["mcplex", "a2aplex", "llmplex", "quotaplex", "clientplex", "skillsplex",
              "access-point", "identity", "usage", "agent", "user", "client"]
LABEL = {"mcplex": "MCPlex", "a2aplex": "A2APlex", "llmplex": "LLMPlex", "quotaplex": "QuotaPlex",
         "clientplex": "ClientPlex", "skillsplex": "SkillsPlex", "access-point": "Access Point",
         "identity": "Identity", "usage": "Usage", "agent": "Agent", "user": "User", "client": "Client"}

FAILURES = []


def icon_parts(name):
    s = open(os.path.join(ROOT, "icons", name + ".svg")).read()
    lines = re.search(r'id="[\w-]+-lines" d="([^"]+)"', s).group(1)
    g = re.search(r'id="[\w-]+-gate" d="([^"]+)"', s)
    return lines, g.group(1) if g else ""


class Doc:
    """One SVG document: collects glyphs and icon symbols, checks text contrast."""

    def __init__(self, fid, w, h, title, desc):
        self.fid, self.w, self.h, self.title, self.desc = fid, w, h, title, desc
        self.ts = Typesetter(prefix="g")
        self.icons = set()
        self.body = []
        self.runs = 0

    def add(self, s):
        self.body.append("  " + s)

    def text(self, x, y, s, size, fill, bg, weight=400, anchor="start", mono=False, tracking=0, rid=None, need=4.5):
        if mono:
            font = "mono-500" if weight >= 500 else "mono-400"
        elif size >= 30:
            font = "display-700" if weight >= 700 else "display-600" if weight >= 600 else "sans-500"
        else:
            font = {400: "sans-400", 500: "sans-500"}.get(weight, "sans-600")
        r = P.contrast(fill, bg)
        self.runs += 1
        if r < need:
            FAILURES.append(f"{self.fid}: '{s}' {fill} on {bg} = {r:.2f} < {need}")
        frag, w = self.ts.run(s, font, size, x, y, fill, anchor, tracking, rid)
        self.add(frag)
        return w

    def width(self, s, size, weight=400, mono=False):
        font = ("mono-500" if weight >= 500 else "mono-400") if mono else (
            ("display-700" if weight >= 700 else "display-600" if weight >= 600 else "sans-500") if size >= 30
            else {400: "sans-400", 500: "sans-500"}.get(weight, "sans-600"))
        return self.ts.width(s, font, size)

    def icon(self, n, x, y, size, color):
        self.icons.add(n)
        self.add(f'<use href="#i-{n}" x="{x}" y="{y}" width="{size}" height="{size}" color="{color}"/>')

    def svg(self):
        sym = []
        for n in ICON_NAMES:
            if n in self.icons:
                l, g = icon_parts(n)
                gate = f'<path d="{g}" fill="currentColor"/>' if g else ""
                sym.append(f'    <symbol id="i-{n}" viewBox="0 0 24 24"><path d="{l}" fill="none" '
                           f'stroke="currentColor" stroke-width="2"/>{gate}</symbol>')
        return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {self.w} {self.h}" width="{self.w}" '
                f'height="{self.h}" role="img" aria-labelledby="{self.fid}-title {self.fid}-desc">\n'
                f'  <title id="{self.fid}-title">{self.title}</title>\n'
                f'  <desc id="{self.fid}-desc">{self.desc}</desc>\n'
                f'  <defs>\n{self.ts.defs()}\n' + "\n".join(sym) + '\n  </defs>\n' +
                "\n".join(self.body) + "\n</svg>\n")

    def write(self, rel):
        path = os.path.join(ROOT, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        open(path, "w").write(self.svg())
        print(f"wrote {rel} ({self.runs} text runs, {os.path.getsize(path) // 1024} KB)")


def mark(x, y, s, lane, gate, extra=""):
    return (f'<g transform="translate({x} {y}) scale({s})"{extra}><path d="{LANES}" fill="none" stroke="{lane}" '
            f'stroke-width="4"/><rect x="14" y="10" width="12" height="12" fill="{gate}"/></g>')


def lockup_h(x, y, s, lane, gate, ink, extra=""):
    return (f'<g transform="translate({x} {y}) scale({s})"{extra}>{mark(0, 0, 1, lane, gate)}'
            f'<path transform="translate(40 4)" d="{WM}" fill="{ink}"/></g>')


STACK_MARK_X = 17.9   # stacked lockup: mark 1.6 units (0.8 at mark scale) left of geometric centre


def lockup_s(x, y, s, lane, gate, ink):
    return (f'<g transform="translate({x} {y}) scale({s})">{mark(STACK_MARK_X, -4, 2, lane, gate)}'
            f'<path transform="translate(2.15 64)" d="{WM}" fill="{ink}"/></g>')


def wordmark(x, y, cap, ink):
    return f'<path transform="translate({x} {y}) scale({cap / 24:.4f})" d="{WM}" fill="{ink}"/>'


# ---------------------------------------------------------------- social card
def social():
    W, H, bg = 1200, 630, N["950"]
    d = Doc("aiplex-social", W, H, "AIPlex",
            "AIPlex social card: the reversed lockup, a one-line description and the six Plex modules with their names.")
    d.add(f'<rect id="background" width="{W}" height="{H}" fill="{bg}"/>')
    d.add(f'<g id="lanes-motif" opacity="0.10">{mark(930, 50, 14, INKD, INKD)}</g>')
    d.add('<g id="lockup">' + lockup_h(80, 120, 3.5, INKD, GATED, INKD) + '</g>')
    d.text(80, 346, "One gateway, identity, policy and usage ledger", 38, INKD, bg, 600)
    d.text(80, 396, "between agents, models, tools and skills.", 38, INKD, bg, 600)
    for i, n in enumerate(ICON_NAMES[:6]):
        x = 80 + i * 178
        d.icon(n, x, 494, 32, P.MODULES[n]["dark"])
        d.text(x + 42, 518, LABEL[n], 21, INKD, bg, 500)
    return d


# ---------------------------------------------------------------- banner
def banner():
    W, H = 1280, 320
    d = Doc("aiplex-banner", W, H, "AIPlex",
            "AIPlex README banner: reversed lockup and description on an Ink panel with the six Plex modules and their "
            "names; the panel carries its own background so it reads on light and dark pages.")
    d.add(f'<rect id="panel" width="{W}" height="{H}" rx="16" fill="{INK}"/>')
    d.add('<g id="lockup">' + lockup_h(72, 92, 2.75, INKD, GATED, INKD) + '</g>')
    d.text(74, 236, "One governed path between agents, models, tools and skills.", 26, N["300"], INK, 400)
    for i, n in enumerate(ICON_NAMES[:6]):
        cx, cy = 884 + (i % 2) * 168, 72 + (i // 2) * 64
        d.add(f'<rect x="{cx}" y="{cy}" width="152" height="52" rx="8" fill="{MISTD}"/>')
        d.icon(n, cx + 14, cy + 12, 28, P.MODULES[n]["dark"])
        d.text(cx + 52, cy + 33, LABEL[n], 19, INKD, MISTD, 500)
    return d


# ---------------------------------------------------------------- brand sheet
def sheet(theme):
    t = THEMES[theme]
    W, H = 1600, 2200
    fid = "aiplex-brand-sheet" + ("-dark" if theme == "dark" else "")
    d = Doc(fid, W, H, "AIPlex brand sheet" + (" (dark)" if theme == "dark" else ""),
            "Overview of the AIPlex identity: mark construction, lockups, clear space and minimum sizes, palette with "
            "contrast ratios, module colours and icons, and usage examples." +
            (" Dark palette version." if theme == "dark" else ""))
    bg, tx, mu = t["bg"], t["text"], t["muted"]
    d.add(f'<rect id="background" width="{W}" height="{H}" fill="{bg}"/>')

    def section(y, num, title, x=64):
        d.text(x, y, num, 14, t["accent"], bg, 600, mono=True)
        d.text(x + 28, y, title, 20, tx, bg, 600)

    d.add(lockup_h(64, 48, 2, t["lane"], t["gate"], tx))
    d.text(1536, 78, "Brand sheet", 22, tx, bg, 600, "end")
    d.text(1536, 104, "Guide and file index: brand/README.md", 14, mu, bg, 400, "end")
    d.add(f'<path d="M64 148H1536" stroke="{t["line"]}" stroke-width="1"/>')

    # 1 construction
    y0 = 196
    section(y0, "1", "Mark construction")
    gx, gy, u = 64, y0 + 28, 14
    grid = []
    for i in range(33):
        c = t["grid2"] if i % 4 == 0 else t["grid"]
        grid.append(f'<path d="M{gx + i*u} {gy}V{gy + 32*u}M{gx} {gy + i*u}H{gx + 32*u}" stroke="{c}" stroke-width="1"/>')
    d.add('<g id="grid">' + "".join(grid) + "</g>")
    d.add(mark(gx, gy, u, t["lane"], t["gate"], ' opacity="0.92"'))
    d.add(f'<path transform="translate({gx} {gy}) scale({u})" d="{LANES}" fill="none" stroke="{bg}" '
          f'stroke-width="0.08" stroke-dasharray="0.3 0.3"/>')
    notes = ["Grid 32 × 32 units; heavy lines every 4 units", "Lane stroke 4 units (1/8 of the grid)",
             "Lane centres at y = 6, 16, 26 (pitch 10)", "Outer lanes turn 45° at x = 6 and meet the gate",
             "Gate 12 × 12 units (3 strokes), x 14–26, y 10–22", "Exit lane 4 units: x 26–30",
             "Live area x 2–30, y 4–28 (28 × 24)", "Butt caps, mitre joins, no rounding",
             "16 px: 1 unit = 0.5 px, lanes 2 px on whole pixels", "Wordmark: cap height 24, stem 4 = lane stroke",
             "Optical: e overshoots 0.4; horizontals 3.5–3.75",
             "Centred alone: mark 0.8 units left of centre"]
    for i, s in enumerate(notes):
        d.text(gx + 32 * u + 32, gy + 18 + i * 30, s, 14, tx if i < 8 else mu, bg)

    # 2 clear space + min size
    x2 = 900
    section(y0, "2", "Clear space and minimum size", x2)
    s = 3
    lx, ly = x2 + 48, y0 + 76
    G = 12 * s
    bx0, by0, bx1, by1 = lx + 2 * s, ly + 2.5 * s, lx + 138.7 * s, ly + 28.4 * s
    ph = (by1 - by0) + 2 * G + 24
    d.add(f'<rect x="{x2}" y="{y0 + 28}" width="636" height="{ph}" fill="{t["panel"]}"/>')
    d.add(f'<rect x="{bx0 - G}" y="{by0 - G}" width="{bx1 - bx0 + 2*G}" height="{by1 - by0 + 2*G}" fill="none" '
          f'stroke="{t["accent"]}" stroke-width="1" stroke-dasharray="4 4"/>')
    d.add(lockup_h(lx, ly, s, t["lane"], t["gate"], tx))
    d.add(f'<rect x="{bx0 - G}" y="{by0 - G}" width="{G}" height="{G}" fill="{t["accent"]}" opacity="0.18"/>')
    d.text(bx0 - G + 8, by0 - G + 23, "G", 14, t["accent"], t["panel"], 600, need=3.0)
    cs_bottom = y0 + 28 + ph
    d.text(x2, cs_bottom + 28, "G = gate width (12 units at mark scale). Keep G clear on every side of any lockup or mark.", 14, tx, bg)
    my = cs_bottom + 76
    d.text(x2, my, "Minimum sizes (shown at actual size)", 14, tx, bg, 600)
    ry = my + 24
    d.add(f'<g transform="translate({x2} {ry})"><path d="M1 3h2l5 5M1 13h2l5-5M1 8h7M12 8h3" fill="none" '
          f'stroke="{t["lane"]}" stroke-width="2"/><rect x="7" y="5" width="6" height="6" fill="{t["gate"]}"/></g>')
    d.text(x2, ry + 50, "Mark 16 px", 13, tx, bg)
    d.text(x2, ry + 68, "favicon drawing", 13, mu, bg)
    d.add(lockup_h(x2 + 150, ry - 2, 20 / 32, t["lane"], t["gate"], tx))
    d.text(x2 + 150, ry + 50, "Horizontal 20 px high", 13, tx, bg)
    d.text(x2 + 150, ry + 68, "(88 px wide)", 13, mu, bg)
    d.add(lockup_s(x2 + 350, ry - 4, 40 / 92, t["lane"], t["gate"], tx))
    d.text(x2 + 350, ry + 50, "Stacked 40 px high", 13, tx, bg)
    d.add(mark(x2 + 520, ry - 4, 24 / 32, t["lane"], t["gate"]))
    d.text(x2 + 520, ry + 50, "Mark 24 px+", 13, tx, bg)
    d.text(x2 + 520, ry + 68, "full drawing", 13, mu, bg)

    # 3 lockups
    y3 = 720
    section(y3, "3", "Lockups and variants")
    panels = [("Colour on light", "#FFFFFF", INK, GATE, INK),
              ("Reversed on dark", N["950"], INKD, GATED, INKD),
              ("Mono (currentColor) on Mist", MIST, INK, INK, INK),
              ("Mono on Gate Blue", GATE, "#FFFFFF", "#FFFFFF", "#FFFFFF")]
    for i, (lab, pbg, la, ga, ink) in enumerate(panels):
        px = 64 + i * 372
        d.add(f'<rect x="{px}" y="{y3 + 24}" width="356" height="236" fill="{pbg}" stroke="{t["line"]}"/>')
        d.add(lockup_h(px + 37, y3 + 56, 2, la, ga, ink))
        d.add(lockup_s(px + 178 - 51.5 * 0.9, y3 + 140, 0.9, la, ga, ink))
        d.text(px, y3 + 284, lab, 14, tx, bg)

    # 4 colour
    y4 = 1060
    section(y4, "4", "Colour (WCAG 2 contrast ratios)")
    sw = [("Gate Blue", GATE, "primary · links, actions, focus", ("on white", "#FFFFFF")),
          ("Gate Blue (dark)", GATED, "primary on dark surfaces", ("on neutral-950", N["950"])),
          ("Ink", INK, "text, lanes of the mark", ("on white", "#FFFFFF")),
          ("Ink (dark)", INKD, "text on dark surfaces", ("on neutral-950", N["950"])),
          ("Mist", MIST, "selection, active nav", ("Ink on Mist", INK)),
          ("Mist (dark)", MISTD, "selection on dark", ("Ink (dark) on it", INKD))]
    for i, (name, hx, use, (lab, other)) in enumerate(sw):
        px = 64 + i * 248
        d.add(f'<rect x="{px}" y="{y4 + 24}" width="232" height="96" fill="{hx}" stroke="{t["line"]}"/>')
        d.text(px, y4 + 144, name, 14, tx, bg, 600)
        d.text(px, y4 + 164, hx, 13, tx, bg, 400, mono=True)
        d.text(px, y4 + 184, use, 13, mu, bg)
        d.text(px, y4 + 204, f"{lab}: {P.contrast(hx, other):.2f}:1", 13, tx, bg)
    ny = y4 + 236
    keys = list(N.keys())
    wN = 1472 / len(keys)
    for i, k in enumerate(keys):
        hx = N[k]
        px = 64 + i * wN
        d.add(f'<rect x="{px:.1f}" y="{ny}" width="{wN:.1f}" height="48" fill="{hx}"/>')
        tc = INK if P.contrast(INK, hx) >= P.contrast("#FFFFFF", hx) else "#FFFFFF"
        d.text(px + 8, ny + 20, k, 12, tc, hx, 600)
        d.text(px + 8, ny + 38, hx, 11, tc, hx, 400, mono=True)
    d.add(f'<rect x="64" y="{ny}" width="1472" height="48" fill="none" stroke="{t["line"]}"/>')
    d.text(64, ny + 70, "Neutrals. Body text: Ink on white 17.26:1; muted text neutral-600 on white 7.93:1; on dark, "
           "Ink (dark) on neutral-950 16.28:1 and neutral-400 6.07:1.", 13, mu, bg)
    sy = ny + 112
    d.text(64, sy, "Status: text colour on its tint, on page background (light | dark)", 14, tx, bg, 600)
    for i, (st, v) in enumerate(P.STATUS.items()):
        px = 64 + i * 372
        for j, mode in enumerate(("light", "dark")):
            bx = px + j * 180
            page = "#FFFFFF" if mode == "light" else N["950"]
            d.add(f'<rect x="{bx}" y="{sy + 16}" width="172" height="92" fill="{page}" stroke="{t["line"]}"/>')
            d.add(f'<rect x="{bx + 12}" y="{sy + 28}" width="96" height="28" rx="4" fill="{v[mode]["bg"]}"/>')
            d.add(f'<rect x="{bx + 22}" y="{sy + 38}" width="8" height="8" fill="{v[mode]["fg"]}"/>')
            d.text(bx + 38, sy + 47, st, 13, v[mode]["fg"], v[mode]["bg"], 600)
            tcol = INK if mode == "light" else INKD
            d.text(bx + 12, sy + 78, v[mode]["fg"], 12, tcol, page, 400, mono=True)
            d.text(bx + 12, sy + 96, f'{P.contrast(v[mode]["fg"], v[mode]["bg"]):.2f} tint · '
                   f'{P.contrast(v[mode]["fg"], page):.2f} page', 11, tcol, page)

    # 5 modules + icons
    y5 = 1560
    section(y5, "5", "Module colours and icon family (24 px grid, 2 px stroke, gate square)")
    for i, n in enumerate(ICON_NAMES[:6]):
        px = 64 + i * 248
        d.add(f'<rect x="{px}" y="{y5 + 24}" width="232" height="132" fill="#FFFFFF" stroke="{t["line"]}"/>')
        d.add(f'<rect x="{px + 116}" y="{y5 + 24}" width="116" height="132" fill="{N["950"]}"/>')
        d.icon(n, px + 34, y5 + 44, 48, P.MODULES[n]["light"])
        d.icon(n, px + 150, y5 + 44, 48, P.MODULES[n]["dark"])
        d.text(px + 12, y5 + 124, P.MODULES[n]["light"], 12, INK, "#FFFFFF", 400, mono=True)
        d.text(px + 128, y5 + 124, P.MODULES[n]["dark"], 12, INKD, N["950"], 400, mono=True)
        d.text(px + 12, y5 + 144, f'{P.contrast(P.MODULES[n]["light"], "#FFFFFF"):.2f}:1', 11, N["600"], "#FFFFFF")
        d.text(px + 128, y5 + 144, f'{P.contrast(P.MODULES[n]["dark"], N["950"]):.2f}:1', 11, N["400"], N["950"])
        d.text(px, y5 + 180, LABEL[n], 14, tx, bg, 600)
    d.text(64, y5 + 206, "Neighbours in navigation order alternate lightness and hue; simulated protan, deutan and "
           "tritan OKLab distance ≥ 0.13 for every adjacent pair.", 13, mu, bg)
    iy = y5 + 236
    for i, n in enumerate(ICON_NAMES[6:]):
        px = 64 + i * 248
        d.add(f'<rect x="{px}" y="{iy}" width="232" height="72" fill="{t["panel"]}"/>')
        d.icon(n, px + 16, iy + 24, 24, tx)
        d.icon(n, px + 60, iy + 28, 16, tx)
        d.text(px + 92, iy + 42, LABEL[n], 14, tx, t["panel"], 600)
    d.text(64, iy + 98, "Access Point, Identity and Usage are shared services; Agent, User and Client are caller kinds. "
           "They use Ink, not a module hue.", 13, mu, bg)

    # 6 usage
    y6 = 1960
    section(y6, "6", "Usage")
    good = [("Colour lockup on white or neutral-50", "#FFFFFF", INK, GATE, INK, ""),
            ("Reversed lockup on dark surfaces", N["950"], INKD, GATED, INKD, ""),
            ("One colour where colour is unavailable", "#FFFFFF", INK, INK, INK, "")]
    bad = [("Colour lockup on dark: lanes disappear", N["950"], INK, GATE, INK, ""),
           ("Recoloured lanes or module hues in the mark", "#FFFFFF", P.MODULES["a2aplex"]["light"],
            P.MODULES["quotaplex"]["light"], INK, ""),
           ("Stretched, rotated or re-spaced", "#FFFFFF", INK, GATE, INK, "stretch")]
    for k, (lab, pbg, la, ga, ink, mod) in enumerate(good + bad):
        px = 64 + k * 248
        ok = k < 3
        d.add(f'<rect x="{px}" y="{y6 + 24}" width="232" height="120" fill="{pbg}" stroke="{t["line"]}"/>')
        if mod == "stretch":
            d.add(f'<g transform="translate({px + 14} {y6 + 70}) scale(1.4 0.8)">{lockup_h(0, 0, 1, la, ga, ink)}</g>')
        else:
            d.add(lockup_h(px + 46, y6 + 68, 1, la, ga, ink))
        c = t["ok"] if ok else t["denied"]
        sym = "M0 6l4 4 8-8" if ok else "M1 1l10 10M11 1L1 11"
        d.add(f'<path transform="translate({px} {y6 + 160})" d="{sym}" fill="none" stroke="{c}" stroke-width="2"/>')
        d.text(px + 20, y6 + 171, "Do" if ok else "Don't", 13, c, bg, 600)
        words, l1, l2 = lab.split(" "), [], []
        for w_ in words:
            (l1 if len(" ".join(l1 + [w_])) <= 30 and not l2 else l2).append(w_)
        d.text(px, y6 + 194, " ".join(l1), 13, tx, bg)
        if l2:
            d.text(px, y6 + 212, " ".join(l2), 13, tx, bg)
    return d


# ---------------------------------------------------------------- mark explorations
def embed(rel, x, y, size, lane, gate):
    """Inline a 32-unit mark file at (x, y), size px, recoloured; ids stripped to stay unique."""
    s = open(os.path.join(ROOT, rel)).read()
    inner = re.search(r"</desc>\n(.*)</svg>", s, re.S).group(1)
    inner = re.sub(r' id="[^"]+"', "", inner)
    inner = inner.replace("#141A2E", "LANE").replace("#2F4FD8", "GATE")
    inner = inner.replace("LANE", lane).replace("GATE", gate)
    inner = " ".join(l.strip() for l in inner.splitlines() if l.strip())
    return f'<g transform="translate({x} {y}) scale({size / 32:g})">{inner}</g>'


def explorations():
    W, H = 1240, 760
    d = Doc("aiplex-mark-explorations", W, H, "AIPlex mark explorations",
            "The current mark beside two explorations, each at 16, 32 and 128 px in colour on light, reversed on "
            "dark and in one colour. The current mark is unchanged; the owner chooses.")
    bg = "#FFFFFF"
    d.add(f'<rect width="{W}" height="{H}" fill="{bg}"/>')
    d.text(40, 52, "Mark explorations (real sizes: 16, 32, 128 px)", 22, INK, bg, 600)
    rows = [("Current", "logo/aiplex-mark.svg", "Three lanes into one gate, one lane out"),
            ("A  Split gate", "explorations/aiplex-mark-alt-a-split-gate.svg", "Merged lane passes between two jaws"),
            ("B  Checkpoint gap", "explorations/aiplex-mark-alt-b-checkpoint.svg", "Lanes merge 2 units short of the gate")]
    cols = [("Colour on light", "#FFFFFF", INK, GATE), ("Reversed on dark", N["950"], INKD, GATED),
            ("One colour", "#FFFFFF", INK, INK)]
    for c, (lab, *_rest) in enumerate(cols):
        d.text(300 + c * 310, 96, lab, 14, N["600"], bg, 600)
    for r, (name, rel, note) in enumerate(rows):
        y = 116 + r * 210
        d.text(40, y + 60, name, 18, INK, bg, 600)
        d.text(40, y + 84, note, 13, N["600"], bg)
        for c, (lab, pbg, la, ga) in enumerate(cols):
            x = 300 + c * 310
            d.add(f'<rect x="{x}" y="{y}" width="290" height="180" fill="{pbg}" stroke="{N["200"]}"/>')
            d.add(embed(rel, x + 16, y + 16, 16, la, ga))
            d.add(embed(rel, x + 16, y + 48, 32, la, ga))
            d.add(embed(rel, x + 146, y + 26, 128, la, ga))
    return d


# ---------------------------------------------------------------- launch graphics
LT = {
    "light": dict(bg="#FFFFFF", text=INK, muted=N["600"], lane=INK, gate=GATE, chip=N["50"], border=N["200"],
                  faint=N["300"], mode="light"),
    "dark": dict(bg=N["950"], text=INKD, muted=N["400"], lane=INKD, gate=GATED, chip=N["900"], border=N["700"],
                 faint=N["600"], mode="dark"),
}
CALLERS = [("agent", "Agents"), ("user", "Users"), ("client", "Clients")]
RESOURCES = [("llmplex", "Models"), ("mcplex", "Tools"), ("a2aplex", "Agents"), ("skillsplex", "Skills")]


def wrap(d, s, size, weight, maxw):
    lines, cur = [], ""
    for w_ in s.split(" "):
        trial = (cur + " " + w_).strip()
        if cur and d.width(trial, size, weight) > maxw:
            lines.append(cur)
            cur = w_
        else:
            cur = trial
    lines.append(cur)
    return lines


def para(d, x, y, s, size, fill, bg, weight=400, maxw=900, leading=1.3):
    lines = wrap(d, s, size, weight, maxw)
    for i, line in enumerate(lines):
        d.text(x, y + i * size * leading, line, size, fill, bg, weight)
    return y + (len(lines) - 1) * size * leading


def chip(d, t, x, y, icon, label, size, icon_color, align="left", sub=None):
    """icon + label chip; (x, y) is the left (or right) edge and vertical centre. Returns (x0, x1)."""
    pad, isz = size * 0.5, size * 1.2
    tw = d.width(label, size, 500)
    if sub:
        tw = max(tw, d.width(sub, size * 0.62, 400))
    w = pad + isz + pad * 0.8 + tw + pad
    h = size * (2.3 if sub else 1.9)
    x0 = x if align == "left" else x - w
    d.add(f'<rect x="{x0:.1f}" y="{y - h/2:.1f}" width="{w:.1f}" height="{h:.1f}" rx="{size*0.3:.1f}" '
          f'fill="{t["chip"]}" stroke="{t["border"]}"/>')
    d.icon(icon, round(x0 + pad, 1), round(y - isz / 2, 1), round(isz, 1), icon_color)
    tx = x0 + pad + isz + pad * 0.8
    if sub:
        d.text(tx, y - size * 0.08, label, size, t["text"], t["chip"], 500)
        d.text(tx, y + size * 0.72, sub, size * 0.62, t["muted"], t["chip"], 400)
    else:
        d.text(tx, y + size * 0.36, label, size, t["text"], t["chip"], 500)
    return x0, x0 + w


def lane_in(x0, y0, x1, y1, stub):
    dy = y1 - y0
    xt = x1 - stub - abs(dy)
    return f"M{x0:.1f} {y0:.1f}H{xt:.1f}l{abs(dy):.1f} {dy:.1f}H{x1:.1f}"


def lane_out(x0, y0, x1, y1, stub):
    dy = y1 - y0
    return f"M{x0:.1f} {y0:.1f}h{stub:.1f}l{abs(dy):.1f} {dy:.1f}H{x1:.1f}"


def flow(d, t, x, y, w, h, size, lane_w, gate, gate_label=None, callers=CALLERS, resources=RESOURCES,
         gate_sub=None, caller_subs=None, resource_subs=None):
    """callers -> one gate -> resources, drawn with the mark's geometry (45 degree lanes, square gate)."""
    cy = y + h / 2
    cpitch = h / len(callers)
    rpitch = h / len(resources)
    cys = [y + cpitch * (i + 0.5) for i in range(len(callers))]
    rys = [y + rpitch * (i + 0.5) for i in range(len(resources))]
    right = []
    for i, (ic, lab) in enumerate(callers):
        right.append(chip(d, t, x, cys[i], ic, lab, size, t["text"],
                          sub=caller_subs[i] if caller_subs else None)[1])
    lefts = []
    for i, (ic, lab) in enumerate(resources):
        lefts.append(chip(d, t, x + w, rys[i], ic, lab, size, P.MODULES[ic][t["mode"]] if ic in P.MODULES else t["text"], align="right",
                          sub=resource_subs[i] if resource_subs else None)[0])
    cr, rl = max(right), min(lefts)
    span_in = max(abs(c - cy) for c in cys)
    span_out = max(abs(r - cy) for r in rys)
    stub = lane_w * 1.5
    room = rl - cr - span_in - span_out - 4 * stub
    g = min(gate, room)
    gx = cr + stub * 2 + span_in + (room - g) / 2
    paths = [lane_in(right[i], c, gx + 1, cy, stub) for i, c in enumerate(cys)]
    paths += [lane_out(gx + g - 1, cy, lefts[i], r, stub) for i, r in enumerate(rys)]
    d.add(f'<path d="{"".join(paths)}" fill="none" stroke="{t["lane"]}" stroke-width="{lane_w}" '
          f'stroke-linejoin="miter"/>')
    d.add(f'<rect x="{gx:.1f}" y="{cy - g/2:.1f}" width="{g:.1f}" height="{g:.1f}" fill="{t["gate"]}"/>')
    if gate_label:
        d.text(gx + g / 2, cy + g / 2 + size * 1.5, gate_label, size, t["text"], t["bg"], 600, "middle")
    if gate_sub:
        d.text(gx + g / 2, cy + g / 2 + size * 2.6, gate_sub, size * 0.8, t["muted"], t["bg"], 400, "middle",
               mono=True)
    return gx, g


def linkedin(mode):
    t = LT[mode]
    W, H = 1200, 627
    d = Doc(f"aiplex-launch-linkedin-{mode}", W, H, "AIPlex",
            "AIPlex: a control plane for agent traffic. Agents, users and clients reach models, tools, agents and "
            "skills through one access point with identity, access, quotas and a usage ledger.")
    d.add(f'<rect width="{W}" height="{H}" fill="{t["bg"]}"/>')
    d.add(lockup_h(64, 52, 1.5, t["lane"], t["gate"], t["text"]))
    d.text(64, 178, "A control plane for agent traffic.", 56, t["text"], t["bg"], 600)
    d.text(64, 232, "Identity, access, quotas and usage in one place.", 32, t["muted"], t["bg"], 500)
    flow(d, t, 64, 282, 1072, 300, 30, 10, 96, "Access point")
    return d


def square(mode):
    t = LT[mode]
    W = H = 1080
    d = Doc(f"aiplex-launch-square-{mode}", W, H, "AIPlex",
            "AIPlex: a control plane for agent traffic. Agents, users and clients reach models, tools, agents and "
            "skills through one access point with identity, access, quotas and a usage ledger.")
    d.add(f'<rect width="{W}" height="{H}" fill="{t["bg"]}"/>')
    d.add(lockup_h(80, 80, 2, t["lane"], t["gate"], t["text"]))
    d.text(80, 280, "A control plane", 76, t["text"], t["bg"], 600)
    d.text(80, 366, "for agent traffic.", 76, t["text"], t["bg"], 600)
    para(d, 80, 446, "Identity, access, quotas and usage in front of models, tools, agents and skills.",
         40, t["muted"], t["bg"], 500, 900)
    flow(d, t, 80, 590, 920, 400, 36, 12, 112, "Access point")
    return d


CAROUSEL_BG = INK


def frame(n, title, body, draw):
    t = dict(LT["dark"], bg=CAROUSEL_BG, chip=MISTD, border=N["700"])
    W = H = 1080
    d = Doc(f"aiplex-launch-carousel-{n}", W, H, f"AIPlex launch carousel {n} of 5: {' '.join(title)}",
            f"{' '.join(title)} {body}")
    d.add(f'<rect width="{W}" height="{H}" fill="{t["bg"]}"/>')
    d.add(lockup_h(80, 72, 1.5, t["lane"], t["gate"], t["text"]))
    d.text(1000, 108, f"{n}/5", 30, N["400"], t["bg"], 500, "end", mono=True)
    for i, line in enumerate(title):
        d.text(80, 270 + i * 88, line, 76, t["text"], t["bg"], 600)
    y = 270 + (len(title) - 1) * 88 + 84
    if body:
        para(d, 80, y, body, 40, N["300"], t["bg"], 400, 920)
    draw(d, t)
    return d


def f_problem(d, t):
    cys = [690, 800, 910]
    rys = [670, 750, 830, 910]
    lines = []
    for c in cys:
        for r in rys:
            lines.append(f"M330 {c}C540 {c} 540 {r} 730 {r}")
    d.add(f'<path d="{"".join(lines)}" fill="none" stroke="{N["500"]}" stroke-width="4"/>')
    for (ic, lab), c in zip(CALLERS, cys):
        chip(d, t, 80, c, ic, lab, 34, t["text"])
    for (ic, lab), r in zip(RESOURCES, rys):
        chip(d, t, 1000, r, ic, lab, 30, P.MODULES[ic]["dark"], align="right")


def f_access(d, t):
    gx, g = flow(d, t, 80, 620, 920, 320, 32, 12, 104, "Access point")
    d.text(540, 1010, "https://<aiplex>/ap/{id}/", 30, N["300"], t["bg"], 500, "middle", mono=True)


def f_ledger(d, t):
    x0, y0 = 80, 690
    cols = [("Caller", 80), ("Resource", 400), ("Tokens", 700), ("Cost", 860)]
    for lab, cx in cols:
        d.text(cx, y0, lab, 28, N["400"], t["bg"], 500)
    d.add(f'<path d="M80 {y0 + 22}H1000" stroke="{N["700"]}" stroke-width="2"/>')
    rows = [("agent", "tutor-agent", "llmplex", "model", 0.8, 0.6),
            ("user", "alice", "mcplex", "tool", 0.3, 0.2),
            ("client", "ide", "skillsplex", "skill", 0.5, 0.35),
            ("agent", "research", "a2aplex", "agent", 0.65, 0.5)]
    for i, (ci, cn, ri, rn, tok, cost) in enumerate(rows):
        y = y0 + 84 + i * 76
        d.icon(ci, 80, y - 26, 34, t["text"])
        d.text(128, y, cn, 32, t["text"], t["bg"], 500, mono=True)
        d.icon(ri, 400, y - 26, 34, P.MODULES[ri]["dark"])
        d.text(448, y, rn, 32, t["text"], t["bg"], 500, mono=True)
        d.add(f'<rect x="700" y="{y - 20}" width="{round(130 * tok)}" height="14" fill="{t["gate"]}"/>')
        d.add(f'<rect x="860" y="{y - 20}" width="{round(130 * cost)}" height="14" fill="{P.MODULES["quotaplex"]["dark"]}"/>')
        d.add(f'<path d="M80 {y + 28}H1000" stroke="{N["800"]}" stroke-width="2"/>')


def f_identity(d, t):
    flow(d, t, 80, 640, 920, 330, 30, 12, 96, "Identity",
         callers=[("agent", "AWS IAM role"), ("agent", "Azure identity"), ("agent", "GitHub Actions")],
         resources=[("identity", "sub: user"), ("agent", "act: agent")])


COMMAND = dict(font="mono-500", size=48, x=176, y=602, fill=INKD, rid="launch-command")


def f_try(d, t):
    x, y, w, h = 80, 480, 920, 170
    d.add(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="12" fill="{N["950"]}" stroke="{N["700"]}"/>')
    d.add(f'<path d="M{x} {y + 44}H{x + w}" stroke="{N["700"]}" stroke-width="2"/>')
    for i in range(3):
        d.add(f'<rect x="{x + 24 + i * 24}" y="{y + 16}" width="12" height="12" fill="{N["600"]}"/>')
    d.text(x + 40, y + 122, "$", 48, N["400"], N["950"], 500, mono=True)
    # the command is one self-contained <path id="launch-command"> so it can be swapped on its own;
    # regenerate with: python3 brand/scripts/outline_text.py --font mono-500 --size 48 --x 176 --y 602 \
    #   --fill "#E8EBF2" --id launch-command "<command>"
    if P.contrast(COMMAND["fill"], N["950"]) < 4.5:
        FAILURES.append("launch-command contrast")
    path, _ = single_path("aiplex init", COMMAND["font"], COMMAND["size"], COMMAND["x"], COMMAND["y"],
                          COMMAND["fill"], rid=COMMAND["rid"])
    d.add(path)
    d.text(80, 730, "Apache 2.0. Source and documentation on GitHub.", 34, N["300"], t["bg"], 400)
    for i, n in enumerate(ICON_NAMES[:6]):
        cx, cy = 80 + (i % 3) * 312, 850 + (i // 3) * 96
        chip(d, t, cx, cy, n, LABEL[n], 30, P.MODULES[n]["dark"])


CAROUSEL = [
    (["Agents reach", "everything on their own."],
     "Every agent, IDE and script holds its own keys. Who called what, as whom and at what cost is spread "
     "across providers.", f_problem),
    (["One URL", "per audience."],
     "An access point puts models, tools and skills behind one stable URL and checks who the caller is before "
     "anything is reached.", f_access),
    (["Who used what,", "in tokens and money."],
     "One append-only usage ledger records each exchange: caller, resource, reported tokens, and cost "
     "when priced.", f_ledger),
    (["One identity", "across clouds."],
     "Workloads on AWS, Azure or GitHub Actions sign in as a registered agent with a token their own "
     "platform issued.", f_identity),
    (["Try it in", "one command."], "", f_try),
]


# ---------------------------------------------------------------- architecture diagram
def architecture(mode):
    t = LT[mode]
    W, H = 1200, 660
    fid = "aiplex-architecture" + ("-dark" if mode == "dark" else "")
    d = Doc(fid, W, H, "AIPlex architecture",
            "Callers (agents, users, clients) reach models, tools, agents and skills through an AIPlex access point, "
            "which applies identity, access, QoS and quota, and writes the usage ledger.")
    d.add(f'<rect width="{W}" height="{H}" fill="{t["bg"]}"/>')
    d.text(40, 56, "Callers", 18, t["muted"], t["bg"], 600)
    d.text(600, 56, "AIPlex access point", 18, t["muted"], t["bg"], 600, "middle")
    d.text(1160, 56, "Resources", 18, t["muted"], t["bg"], 600, "end")
    # access point panel
    px, py, pw, ph = 420, 92, 360, 528
    d.add(f'<rect x="{px}" y="{py}" width="{pw}" height="{ph}" rx="12" fill="{t["chip"]}" stroke="{t["gate"]}" stroke-width="2"/>')
    d.add(f'<rect x="{px}" y="{py}" width="{pw}" height="64" rx="12" fill="{t["gate"]}"/>')
    d.add(f'<rect x="{px}" y="{py + 52}" width="{pw}" height="12" fill="{t["gate"]}"/>')
    on = P.ON_GATE[mode]
    d.icon("access-point", px + 20, py + 18, 28, on)
    d.text(px + 60, py + 40, "One URL per audience", 20, on, t["gate"], 600)
    stages = [("identity", "Identity", "Who is calling, as which agent"),
              ("access-point", "Access", "Grants, caller kinds, subjects"),
              ("quotaplex", "QoS and quota", "Rate, concurrency, budgets"),
              ("usage", "Usage ledger", "Tokens and cost per exchange")]
    for i, (ic, lab, sub) in enumerate(stages):
        y = py + 96 + i * 108
        d.add(f'<rect x="{px + 20}" y="{y}" width="{pw - 40}" height="88" rx="8" fill="{t["bg"]}" stroke="{t["border"]}"/>')
        col = P.MODULES["quotaplex"][mode] if ic == "quotaplex" else t["text"]
        d.icon(ic, px + 40, y + 28, 32, col)
        d.text(px + 92, y + 40, lab, 20, t["text"], t["bg"], 600)
        d.text(px + 92, y + 66, sub, 15, t["muted"], t["bg"], 400)
        if i < 3:
            d.add(f'<path d="M{px + pw/2} {y + 88}v20" stroke="{t["lane"]}" stroke-width="3"/>')
    # callers
    csub = ["A2A, SDKs, services", "Console, CLI", "IDEs, CLIs (ClientPlex)"]
    cys = [196, 356, 516]
    rights = []
    for (ic, lab), sub, cy in zip(CALLERS, csub, cys):
        rights.append(chip(d, t, 40, cy, ic, lab, 20, t["text"], sub=sub)[1])
    rsub = [("llmplex", "Models", "LLMPlex"), ("mcplex", "Tools", "MCPlex"),
            ("a2aplex", "Agents", "A2APlex"), ("skillsplex", "Skills", "SkillsPlex")]
    rys = [176, 296, 416, 536]
    lefts = []
    for (ic, lab, sub), ry in zip(rsub, rys):
        lefts.append(chip(d, t, 1160, ry, ic, lab, 20, P.MODULES[ic][mode], align="right", sub=sub)[0])
    cy = py + ph / 2
    paths = [lane_in(r, c, px, cy, 14) for r, c in zip(rights, cys)]
    paths += [lane_out(px + pw, cy, l_, r, 14) for l_, r in zip(lefts, rys)]
    d.add(f'<path d="{"".join(paths)}" fill="none" stroke="{t["lane"]}" stroke-width="4" stroke-linejoin="miter"/>')
    d.add(f'<rect x="{px - 8}" y="{cy - 8}" width="16" height="16" fill="{t["gate"]}"/>')
    d.add(f'<rect x="{px + pw - 8}" y="{cy - 8}" width="16" height="16" fill="{t["gate"]}"/>')
    d.text(600, 648, "QuotaPlex sets the budgets the access point enforces; ClientPlex configures the clients.",
           15, t["muted"], t["bg"], 400, "middle")
    return d


def build(which):
    out = []
    if "social" in which:
        out += [(social(), "social/aiplex-social-card.svg"), (banner(), "social/aiplex-readme-banner.svg")]
    if "sheet" in which:
        out += [(sheet("light"), "brand-sheet.svg"), (sheet("dark"), "brand-sheet-dark.svg")]
    if "explorations" in which:
        out += [(explorations(), "explorations/aiplex-mark-explorations.svg")]
    if "launch" in which:
        for m in ("light", "dark"):
            out += [(linkedin(m), f"launch/aiplex-linkedin-{m}.svg"), (square(m), f"launch/aiplex-square-{m}.svg")]
        for i, (title, body, draw) in enumerate(CAROUSEL, 1):
            out += [(frame(i, title, body, draw), f"launch/aiplex-carousel-{i}.svg")]
    if "architecture" in which:
        out += [(architecture("light"), "launch/architecture.svg"),
                (architecture("dark"), "launch/architecture-dark.svg")]
    return out


if __name__ == "__main__":
    targets = [a for a in sys.argv[1:] if not a.startswith("-")] or \
        ["social", "sheet", "explorations", "launch", "architecture"]
    docs = build(targets)
    if "--check" not in sys.argv:
        for d, rel in docs:
            d.write(rel)
    if FAILURES:
        print("TEXT CONTRAST FAILURES:")
        for f in FAILURES:
            print("  " + f)
        sys.exit(1)
    print("text contrast: every run >= its minimum")
