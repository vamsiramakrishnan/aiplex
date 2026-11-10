#!/usr/bin/env python3
"""AIPlex colour system: single source for brand/tokens/*.

Run from anywhere (standard library only):

    python3 brand/scripts/palette.py            # check + print report
    python3 brand/scripts/palette.py --write    # also regenerate tokens.css / tokens.json

What it checks
  * WCAG 2.x contrast ratio for every recommended text/background pairing
    (AA: 4.5:1 for text, 3:1 for graphics such as icons, focus rings, chart marks).
  * Module hues: each adjacent pair in navigation order, and every pair overall,
    is simulated for protanopia, deuteranopia and tritanopia (Machado, Oliveira &
    Fernandes 2009, severity 1.0, applied in linear sRGB) and the OKLab distance
    between the simulated colours is reported. The script fails if an adjacent
    pair falls below MIN_ADJACENT_DE in any simulation.
Exit status is non-zero if any check fails.
"""
import json
import math
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
TOKENS = os.path.join(HERE, "..", "tokens")

# ---------------------------------------------------------------- colour maths

def hex_to_rgb(h):
    h = h.lstrip("#")
    return tuple(int(h[i:i + 2], 16) / 255 for i in (0, 2, 4))


def rgb_to_hex(rgb):
    return "#" + "".join(f"{round(max(0, min(1, c)) * 255):02X}" for c in rgb)


def to_lin(c):
    return c / 12.92 if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4


def from_lin(c):
    return 12.92 * c if c <= 0.0031308 else 1.055 * c ** (1 / 2.4) - 0.055


def luminance(h):
    r, g, b = (to_lin(c) for c in hex_to_rgb(h))
    return 0.2126 * r + 0.7152 * g + 0.0722 * b


def contrast(a, b):
    la, lb = sorted((luminance(a), luminance(b)), reverse=True)
    return (la + 0.05) / (lb + 0.05)


def lin_to_oklab(r, g, b):
    l = 0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b
    m = 0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b
    s = 0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b
    l, m, s = (math.copysign(abs(v) ** (1 / 3), v) for v in (l, m, s))
    return (0.2104542553 * l + 0.7936177850 * m - 0.0040720468 * s,
            1.9779984951 * l - 2.4285922050 * m + 0.4505937099 * s,
            0.0259040371 * l + 0.7827717662 * m - 0.8086757660 * s)


def oklab_to_lin(L, a, b):
    l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
    m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
    s = (L - 0.0894841775 * a - 1.2914855480 * b) ** 3
    return (4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
            -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
            -0.0041960863 * l - 0.7034186147 * m + 1.7076147010 * s)


def oklch(L, C, H):
    """OKLCH -> hex, reducing chroma until the colour is inside sRGB."""
    while C >= 0:
        a, b = C * math.cos(math.radians(H)), C * math.sin(math.radians(H))
        rgb = oklab_to_lin(L, a, b)
        if all(-1e-4 <= c <= 1 + 1e-4 for c in rgb):
            return rgb_to_hex([from_lin(max(0, min(1, c))) for c in rgb])
        C -= 0.002
    raise ValueError("out of gamut")


MACHADO = {
    "protan": ((0.152286, 1.052583, -0.204868), (0.114503, 0.786281, 0.099216), (-0.003882, -0.048116, 1.051998)),
    "deutan": ((0.367322, 0.860646, -0.227968), (0.280085, 0.672501, 0.047413), (-0.011820, 0.042940, 0.968881)),
    "tritan": ((1.255528, -0.076749, -0.178779), (-0.078411, 0.930809, 0.147602), (0.004733, 0.691367, 0.303900)),
}


def simulate(h, kind):
    lin = [to_lin(c) for c in hex_to_rgb(h)]
    if kind == "normal":
        return lin
    return [max(0, min(1, sum(m * c for m, c in zip(row, lin)))) for row in MACHADO[kind]]


def de_ok(h1, h2, kind):
    a = lin_to_oklab(*simulate(h1, kind))
    b = lin_to_oklab(*simulate(h2, kind))
    return math.dist(a, b)


# ---------------------------------------------------------------- the palette
# Hand-picked values. Module hues are generated from OKLCH so that they share
# chroma and step through hue, alternating lightness so neighbours stay apart
# even when hue information is lost.

NEUTRAL = {
    "0": "#FFFFFF", "50": "#F6F7F9", "100": "#ECEEF2", "200": "#D8DCE4",
    "300": "#B6BDCB", "400": "#8890A3", "500": "#636B7E", "600": "#4A5163",
    "700": "#343947", "800": "#222632", "850": "#191C26", "900": "#12151D",
    "950": "#0B0D13",
}

BRAND = {
    # Gate Blue: the governed point in the mark. Primary actions, links, focus.
    "gate": {"light": "#2F4FD8", "dark": "#8DA2FF"},
    # Ink: lanes in the mark, body text on light surfaces.
    "ink": {"light": "#141A2E", "dark": "#E8EBF2"},
    # Mist: tinted surface for selection and the active navigation item.
    "mist": {"light": "#EDF0FD", "dark": "#1B2142"},
}

STATUS = {  # fg = text/icon colour, bg = badge tint
    "ok":      {"light": {"fg": "#167044", "bg": "#E5F4EC"}, "dark": {"fg": "#5CCB93", "bg": "#0F2A1E"}},
    "warning": {"light": {"fg": "#8F5300", "bg": "#FDF1DC"}, "dark": {"fg": "#F2B544", "bg": "#2E2210"}},
    "denied":  {"light": {"fg": "#B8202F", "bg": "#FCE8EA"}, "dark": {"fg": "#FF8A92", "bg": "#3A1519"}},
    "unknown": {"light": {"fg": "#5A6275", "bg": "#ECEEF2"}, "dark": {"fg": "#A3ABBD", "bg": "#222632"}},
}

# Navigation order matters: adjacent entries are checked hardest.
MODULE_SPEC = [
    # name,        hue, light L, dark L
    ("mcplex",     245, 0.50, 0.72),   # blue
    ("a2aplex",     60, 0.62, 0.84),   # amber-orange
    ("llmplex",    305, 0.48, 0.72),   # violet
    ("quotaplex",  185, 0.62, 0.86),   # teal
    ("clientplex",   5, 0.48, 0.72),   # rose
    ("skillsplex", 130, 0.62, 0.86),   # olive green
]
MODULE_CHROMA = 0.15
MODULES = {
    name: {"light": oklch(lL, MODULE_CHROMA, h), "dark": oklch(dL, MODULE_CHROMA * 0.85, h)}
    for name, h, lL, dL in MODULE_SPEC
}
MIN_ADJACENT_DE = 0.10   # OKLab; several times a just-noticeable difference (~0.02)

SURFACES = {
    "light": {"bg": NEUTRAL["0"], "surface": NEUTRAL["50"], "surface-raised": NEUTRAL["0"],
              "border": NEUTRAL["200"], "text": BRAND["ink"]["light"], "text-muted": NEUTRAL["600"]},
    "dark":  {"bg": NEUTRAL["950"], "surface": NEUTRAL["900"], "surface-raised": NEUTRAL["850"],
              "border": NEUTRAL["700"], "text": BRAND["ink"]["dark"], "text-muted": NEUTRAL["400"]},
}
ON_GATE = {"light": "#FFFFFF", "dark": NEUTRAL["950"]}


# ---------------------------------------------------------------- primary tonal scale
# Computed in OKLCH around Gate Blue's hue. Step 600 is the primary itself in each
# mode (#2F4FD8 light, #8DA2FF dark); the other steps follow a lightness ladder.
# Light: 50 is the palest tint, 950 the deepest shade. Dark: the ladder is
# reversed so that the same step plays the same role on a dark page (50 = the
# most subtle tint against neutral-950, 950 = nearly white). A component that
# uses primary-100 for a selected background and primary-600 for its text
# therefore works in both modes. Chroma follows the anchor and tapers towards
# the ends of the ladder, then is reduced further if needed to stay in sRGB.

def hex_to_oklch(h):
    L, a, b = lin_to_oklab(*[to_lin(c) for c in hex_to_rgb(h)])
    return L, math.hypot(a, b), math.degrees(math.atan2(b, a)) % 360


STEPS = ["50", "100", "200", "300", "400", "500", "600", "700", "800", "900", "950"]
PRIMARY_LADDER = {
    "light": [0.975, 0.945, 0.89, 0.80, 0.70, 0.60, None, 0.44, 0.37, 0.30, 0.24],
    "dark":  [0.19, 0.23, 0.29, 0.37, 0.46, 0.61, None, 0.80, 0.86, 0.92, 0.96],
}


def primary_scale(mode):
    anchor = BRAND["gate"][mode]
    L0, C0, H0 = hex_to_oklch(anchor)
    out = {}
    for step, L in zip(STEPS, PRIMARY_LADDER[mode]):
        if L is None:
            out[step] = anchor
            continue
        taper = max(0.12, 1 - abs(L - L0) * (1.5 if mode == "light" else 0.9))
        if mode == "dark":
            taper *= min(1.0, L / 0.45)  # keep the darkest tints near-neutral against neutral-950
        out[step] = oklch(L, C0 * taper, H0)
    return out


PRIMARY = {m: primary_scale(m) for m in ("light", "dark")}


def best_on(bg):
    """Text colour for a filled step: white or neutral-950, whichever contrasts more."""
    w, k = contrast("#FFFFFF", bg), contrast(NEUTRAL["950"], bg)
    return ("#FFFFFF", w) if w >= k else (NEUTRAL["950"], k)


# Semantic aliases: a product can theme itself from these alone.
ALIASES = {
    mode: {
        "bg": SURFACES[mode]["bg"],
        "surface": SURFACES[mode]["surface"],
        "surface-raised": SURFACES[mode]["surface-raised"],
        "border": SURFACES[mode]["border"],
        "text": SURFACES[mode]["text"],
        "text-muted": SURFACES[mode]["text-muted"],
        "primary": PRIMARY[mode]["600"],
        "primary-hover": PRIMARY[mode]["700"],
        "primary-contrast": ON_GATE[mode],
        "primary-subtle": PRIMARY[mode]["100"],
        "focus-ring": PRIMARY[mode]["600"],
        **{f"status-{n}-{k}": v[mode][k] for n, v in STATUS.items() for k in ("fg", "bg")},
    }
    for mode in ("light", "dark")
}


def primary_step_rows():
    rows = []
    for mode in ("light", "dark"):
        page = SURFACES[mode]["bg"]
        for step, hx in PRIMARY[mode].items():
            on, r = best_on(hx)
            rows.append({"mode": mode, "step": step, "hex": hx,
                         "best_text_on_step": on, "ratio_text_on_step": round(r, 2),
                         "white_on_step": round(contrast("#FFFFFF", hx), 2),
                         "neutral950_on_step": round(contrast(NEUTRAL["950"], hx), 2),
                         "step_as_text_on_page": round(contrast(hx, page), 2)})
    return rows


def pairings():
    """(mode, foreground name, fg, background name, bg, minimum)"""
    out = []
    for mode in ("light", "dark"):
        s = SURFACES[mode]
        for bgname in ("bg", "surface", "surface-raised"):
            out.append((mode, "text", s["text"], bgname, s[bgname], 4.5))
            out.append((mode, "text-muted", s["text-muted"], bgname, s[bgname], 4.5))
            out.append((mode, "gate (link)", BRAND["gate"][mode], bgname, s[bgname], 4.5))
        out.append((mode, "on-gate", ON_GATE[mode], "gate (button)", BRAND["gate"][mode], 4.5))
        out.append((mode, "gate (link)", BRAND["gate"][mode], "mist", BRAND["mist"][mode], 4.5))
        out.append((mode, "text", s["text"], "mist", BRAND["mist"][mode], 4.5))
        for st, v in STATUS.items():
            out.append((mode, f"status-{st}", v[mode]["fg"], "bg", s["bg"], 4.5))
            out.append((mode, f"status-{st}", v[mode]["fg"], "surface", s["surface"], 4.5))
            out.append((mode, f"status-{st}", v[mode]["fg"], f"status-{st}-bg", v[mode]["bg"], 4.5))
        for m, v in MODULES.items():
            out.append((mode, f"module-{m} (graphic)", v[mode], "bg", s["bg"], 3.0))
            out.append((mode, f"module-{m} (graphic)", v[mode], "surface", s["surface"], 3.0))
        out.append((mode, "border (graphic, decorative)", s["border"], "bg", s["bg"], None))
        a = ALIASES[mode]
        for fg_name, bg_name in (("primary-contrast", "primary"), ("primary-contrast", "primary-hover"),
                                 ("primary", "bg"), ("primary", "surface"), ("primary-hover", "bg"),
                                 ("primary", "primary-subtle"), ("text", "primary-subtle")):
            out.append((mode, f"alias {fg_name}", a[fg_name], f"alias {bg_name}", a[bg_name], 4.5))
    return out


def report():
    ok = True
    print("## Contrast (WCAG 2.x)\n")
    print("| mode | foreground | hex | background | hex | ratio | needs | result |")
    print("|---|---|---|---|---|---|---|---|")
    rows = []
    for mode, fn, fg, bn, bg, need in pairings():
        r = contrast(fg, bg)
        res = "n/a" if need is None else ("pass" if r >= need else "FAIL")
        if res == "FAIL":
            ok = False
        rows.append({"mode": mode, "fg": fn, "fg_hex": fg, "bg": bn, "bg_hex": bg,
                     "ratio": round(r, 2), "min": need, "result": res})
        print(f"| {mode} | {fn} | {fg} | {bn} | {bg} | {r:.2f} | {need or '-'} | {res} |")

    print("\n## Module hues under colour-vision simulation (OKLab distance)\n")
    names = [n for n, *_ in MODULE_SPEC]
    kinds = ["normal", "protan", "deutan", "tritan"]
    cvd = []
    for mode in ("light", "dark"):
        print(f"\n{mode}: adjacent pairs in navigation order")
        print("| pair | " + " | ".join(kinds) + " |")
        print("|---|" + "---|" * len(kinds))
        for a, b in zip(names, names[1:]):
            ds = [de_ok(MODULES[a][mode], MODULES[b][mode], k) for k in kinds]
            if min(ds) < MIN_ADJACENT_DE:
                ok = False
            cvd.append({"mode": mode, "pair": f"{a}/{b}", **{k: round(d, 3) for k, d in zip(kinds, ds)}})
            print(f"| {a} / {b} | " + " | ".join(f"{d:.3f}" for d in ds) + " |")
        worst = min((de_ok(MODULES[a][mode], MODULES[b][mode], k), a, b, k)
                    for i, a in enumerate(names) for b in names[i + 1:] for k in kinds)
        print(f"worst of all {len(names) * (len(names) - 1) // 2} pairs ({mode}): "
              f"{worst[1]}/{worst[2]} under {worst[3]} = {worst[0]:.3f}")
    print(f"\nminimum required for adjacent pairs: {MIN_ADJACENT_DE}")

    print("\n## Primary tonal scale: best text colour on each step, and the step as text on the page\n")
    print("| mode | step | hex | text on step | ratio | step on page |")
    print("|---|---|---|---|---|---|")
    for r in primary_step_rows():
        print(f"| {r['mode']} | {r['step']} | {r['hex']} | {r['best_text_on_step']} | "
              f"{r['ratio_text_on_step']:.2f} | {r['step_as_text_on_page']:.2f} |")
    print("\nRESULT:", "all checks pass" if ok else "FAILURES above")
    return ok, rows, cvd


# ---------------------------------------------------------------- token output

def token_tree():
    return {
        "$description": "AIPlex brand tokens. Generated by brand/scripts/palette.py; edit there.",
        "color": {
            "neutral": NEUTRAL,
            "brand": BRAND,
            "status": STATUS,
            "module": MODULES,
            "primary": PRIMARY,
            "alias": ALIASES,
            "semantic": {
                mode: {**SURFACES[mode], "gate": BRAND["gate"][mode], "on-gate": ON_GATE[mode],
                       "mist": BRAND["mist"][mode], "focus-ring": BRAND["gate"][mode]}
                for mode in ("light", "dark")
            },
        },
        "font": {
            "sans": "Inter, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif",
            "mono": "'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
        },
    }


def css():
    t = token_tree()["color"]
    lines = ["/* AIPlex brand tokens. Generated by brand/scripts/palette.py; do not edit by hand. */",
             "/* Light is the default. Dark applies with prefers-color-scheme: dark, or",
             "   explicitly with data-theme=\"dark\" on an ancestor; data-theme=\"light\" forces light. */", ""]

    def block(mode):
        s = t["semantic"][mode]
        b = [f"  --aiplex-{k}: {v};" for k, v in s.items()]
        b += [f"  --aiplex-status-{n}: {v[mode]['fg']};\n  --aiplex-status-{n}-bg: {v[mode]['bg']};"
              for n, v in STATUS.items()]
        b += [f"  --aiplex-module-{n}: {v[mode]};" for n, v in MODULES.items()]
        b += [f"  --aiplex-primary-{k}: {v};" for k, v in PRIMARY[mode].items()]
        b += [f"  --aiplex-color-{k}: {v};" for k, v in ALIASES[mode].items()]
        return b

    lines.append(":root {")
    lines += [f"  --aiplex-neutral-{k}: {v};" for k, v in NEUTRAL.items()]
    lines += [f"  --aiplex-brand-{n}-{m}: {v[m]};" for n, v in BRAND.items() for m in ("light", "dark")]
    lines += [f"  --aiplex-primary-{m}-{k}: {v};" for m in ("light", "dark") for k, v in PRIMARY[m].items()]
    f = token_tree()["font"]
    lines += [f"  --aiplex-font-sans: {f['sans']};", f"  --aiplex-font-mono: {f['mono']};"]
    lines += ["", "  color-scheme: light dark;"]
    lines += block("light")
    lines.append("}\n")
    lines.append("@media (prefers-color-scheme: dark) {\n  :root:not([data-theme=\"light\"]) {")
    lines += ["  " + l for l in block("dark")]
    lines.append("  }\n}\n")
    lines.append("[data-theme=\"dark\"] {")
    lines += block("dark")
    lines.append("}\n")
    lines.append("[data-theme=\"light\"] {")
    lines += block("light")
    lines.append("}")
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    passed, rows, cvd = report()
    if "--write" in sys.argv:
        tree = token_tree()
        tree["checks"] = {"contrast": rows, "primary_steps": primary_step_rows(), "module_cvd_adjacent": cvd,
                          "module_cvd_min_adjacent_oklab": MIN_ADJACENT_DE}
        os.makedirs(TOKENS, exist_ok=True)
        with open(os.path.join(TOKENS, "tokens.json"), "w") as fh:
            json.dump(tree, fh, indent=2)
            fh.write("\n")
        with open(os.path.join(TOKENS, "tokens.css"), "w") as fh:
            fh.write(css())
        print("wrote brand/tokens/tokens.css and brand/tokens/tokens.json")
    sys.exit(0 if passed else 1)
