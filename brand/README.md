# AIPlex brand

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="brand-sheet-dark.svg">
  <img alt="AIPlex brand sheet" src="brand-sheet.svg">
</picture>

## The mark

Three lanes converge into one square gate and leave as one lane. The lanes are callers (agents, users, clients). The gate is the single governed point: one gateway, one identity and policy layer, one usage ledger. The lane on the right is governed traffic going on to models, tools, agents and skills. The mark is a symbol, not a letter, and it has no gradients, glow or decoration.

### Construction

- Grid: 32 × 32 units. Live area x 2–30, y 4–28 (28 × 24).
- Lane stroke: 4 units (1/8 of the grid). Butt caps, mitre joins, no rounded corners.
- Lane centres at y = 6, 16, 26 (pitch 10). The outer lanes run 4 units horizontally from x = 2, then turn 45° and meet the gate.
- Gate: 12 × 12 units (three lane widths) at x 14–26, y 10–22. Exit lane: x 26–30.
- Every edge sits on a whole unit, so at 16 px (1 unit = 0.5 px) the horizontal edges land on whole pixels and the lanes are 2 px wide. `aiplex-favicon.svg` is the same drawing on a 16-unit grid with explicit 2 px strokes.
- Optical corrections: wherever the mark is centred on its own axis (app icon, stacked lockup) it sits 0.8 mark units left of geometric centre. The gate is the heaviest element and lies right of the mark's centre, so a geometrically centred mark looks pushed right. The horizontal lockup needs no correction.

### Wordmark

"AIPlex" is drawn as outlines; it is not set in a font. Cap height 24 units, stem 4 units, the same as the mark's lane stroke at 1:1. The x-height is 17 units and the `l` rises 1.5 units above the cap height.

- One word, with capitals A, I and P. The `I` has slab bars so that "AI" cannot be read as "Al" and the `I` cannot be confused with the `l`.
- Straight terminals and 45°-family diagonals match the mark. The `e` is a circle with a horizontal bar and a radial cut, and it overshoots the x-height and baseline by 0.4 units. Horizontal strokes are 3.5–3.75 units so they look the same weight as the 4-unit verticals.
- Do not retype the wordmark in a font. Use the files.

### Lockups

- Horizontal: the mark at 1:1 next to the wordmark. The mark's outer lanes align with the cap height and the baseline. The gap is 10 units.
- Stacked: the mark at 2× sits above the wordmark with a 12-unit gap, optically centred: it is 1.6 lockup units (0.8 mark units) left of the geometric centre (`translate(17.9 -4)`). The first version centred it geometrically (`translate(19.5 -4)`), and at display sizes the mark looked right-heavy over the wordmark. The correction matches the app icon, so the geometric version was replaced, not kept as a second variant. It remains in git history.
- Clear space: **G** = the gate width (12 units at the scale of the mark in use). Keep G clear on every side.

### Minimum sizes

| Asset | Minimum | Notes |
|---|---|---|
| Mark | 16 px | Use `aiplex-favicon.svg` from 16 to 23 px and the full mark from 24 px. |
| Horizontal lockup | 20 px high (88 px wide) | Below this, use the mark alone. |
| Stacked lockup | 40 px high | The wordmark is half the mark's height, so below 40 px it is under 10 px tall and unreadable. Use the horizontal lockup or the mark alone below 40 px. |
| Module icons | 16 px | Drawn for 24 px. They stay legible at 16 px but are not pixel-snapped at that size. |

## Colour

Colour is added on top of the drawing; the drawing does not depend on it. The mark works in one colour (`-mono` files use `currentColor`).

| Role | Light | Dark | Use |
|---|---|---|---|
| Gate Blue (primary) | `#2F4FD8` | `#8DA2FF` | Gate in the mark, primary actions, links, focus ring |
| Ink (supporting) | `#141A2E` | `#E8EBF2` | Text, lanes in the mark |
| Mist (supporting) | `#EDF0FD` | `#1B2142` | Selection, active navigation item |
| Neutrals | `#FFFFFF` `#F6F7F9` `#ECEEF2` `#D8DCE4` `#B6BDCB` `#8890A3` `#636B7E` `#4A5163` `#343947` `#222632` `#191C26` `#12151D` `#0B0D13` | | Surfaces, borders, muted text (0–950) |

Status colours. Show each one with an icon or a label as well as colour.

| Status | Light text / tint | Dark text / tint |
|---|---|---|
| ok | `#167044` / `#E5F4EC` | `#5CCB93` / `#0F2A1E` |
| warning | `#8F5300` / `#FDF1DC` | `#F2B544` / `#2E2210` |
| denied | `#B8202F` / `#FCE8EA` | `#FF8A92` / `#3A1519` |
| unknown | `#5A6275` / `#ECEEF2` | `#A3ABBD` / `#222632` |

### Contrast

`brand/scripts/palette.py` computes these WCAG 2 ratios. AA needs 4.5:1 for text and 3:1 for graphics. Every recommended pairing passes.

| Pairing | Light | Dark |
|---|---|---|
| Text (Ink) on bg / surface / raised | 17.26 / 16.10 / 17.26 | 16.28 / 15.29 / 14.24 |
| Muted text (neutral-600 / neutral-400) on bg / surface / raised | 7.93 / 7.40 / 7.93 | 6.07 / 5.71 / 5.31 |
| Gate Blue link on bg / surface / raised | 6.50 / 6.06 / 6.50 | 8.09 / 7.60 / 7.08 |
| Button label on Gate Blue (white / neutral-950) | 6.50 | 8.09 |
| Gate Blue on Mist; Ink on Mist | 5.72; 15.19 | 6.51; 13.10 |
| ok on bg / surface / tint | 6.11 / 5.70 / 5.38 | 9.64 / 9.05 / 7.60 |
| warning on bg / surface / tint | 6.17 / 5.75 / 5.52 | 10.61 / 9.97 / 8.48 |
| denied on bg / surface / tint | 6.39 / 5.96 / 5.44 | 8.60 / 8.08 / 7.15 |
| unknown on bg / surface / tint | 6.11 / 5.70 / 5.26 | 8.43 / 7.92 / 6.55 |

Light bg = `#FFFFFF`, surface = `#F6F7F9`, raised = `#FFFFFF`. Dark bg = `#0B0D13`, surface = `#12151D`, raised = `#191C26`. Borders (`#D8DCE4` / `#343947`) are decorative and are not used as the only indicator of anything.

### Module colours

There is one hue per Plex, for navigation, diagrams and icons. All share OKLCH chroma 0.15 (0.13 in dark mode). Hue changes in steps, and lightness alternates between neighbours, so adjacent items differ in lightness even when hue is not seen.

| Module (navigation order) | Light | Ratio on white | Dark | Ratio on `#0B0D13` |
|---|---|---|---|---|
| MCPlex | `#0568A4` | 5.96 | `#58ACEF` | 7.92 |
| A2APlex | `#C36D05` | 3.81 | `#FFBA81` | 11.65 |
| LLMPlex | `#7241A0` | 7.05 | `#B78FE4` | 7.46 |
| QuotaPlex | `#099B8F` | 3.44 | `#57ECDC` | 13.35 |
| ClientPlex | `#9E2D52` | 7.12 | `#E6819A` | 7.37 |
| SkillsPlex | `#679725` | 3.48 | `#B5E287` | 13.13 |

Module colours meet 3:1 for graphics. Several are below 4.5:1, so module labels use Ink or text colour, not the module hue. Module hues never mean status. Access Point, Identity, Usage and the caller kinds (agent, user, client) use Ink.

**How the colour-vision check works.** Each colour is simulated for protanopia, deuteranopia and tritanopia with the Machado, Oliveira and Fernandes (2009) matrices at full severity, applied in linear sRGB. The script then measures the OKLab distance between neighbours. The script fails below 0.10, about five times a just-noticeable difference. The smallest adjacent distance is 0.136 (QuotaPlex/ClientPlex under deuteranopia, both modes). Some pairs that are not adjacent come closer: MCPlex/LLMPlex under deuteranopia is 0.018 in light mode. Do not rely on module colour alone: always show the module icon and name.

### Primary tonal scale and aliases

`palette.py` computes a primary scale (50–950) in OKLCH around the Gate Blue hue. Step 600 is the primary in each mode.

- **Light:** 50 is the palest tint and 950 the deepest shade.
- **Dark:** the ladder is reversed. 50 is the most subtle tint against neutral-950 and 950 is nearly white. The same step therefore plays the same role in both modes. For example, `--aiplex-primary-100` is a selected-row background and `--aiplex-primary-600` is the accent.

| Step | Light | Text on it | Ratio | Dark | Text on it | Ratio |
|---|---|---|---|---|---|---|
| 50 | `#F3F7FF` | neutral-950 | 18.09 | `#0F1321` | white | 18.50 |
| 100 | `#E5EDFE` | neutral-950 | 16.53 | `#171C2F` | white | 16.89 |
| 200 | `#CBDAFF` | neutral-950 | 13.88 | `#232946` | white | 14.22 |
| 300 | `#A2BCFF` | neutral-950 | 10.33 | `#333C67` | white | 10.59 |
| 400 | `#7699FA` | neutral-950 | 7.13 | `#455291` | white | 7.32 |
| 500 | `#5176EA` | neutral-950 | 4.74 | `#6B7DCC` | neutral-950 | 5.02 |
| 600 | `#2F4FD8` | white | 6.50 | `#8DA2FF` | neutral-950 | 8.09 |
| 700 | `#2541BB` | white | 8.23 | `#A8BAFE` | neutral-950 | 10.28 |
| 800 | `#1A2F98` | white | 11.03 | `#C2CFFE` | neutral-950 | 12.62 |
| 900 | `#0F1E77` | white | 14.26 | `#DCE4FF` | neutral-950 | 15.34 |
| 950 | `#07105C` | white | 16.97 | `#EEF1FE` | neutral-950 | 17.25 |

"Text on it" is the better of white and neutral-950 (`#0B0D13`). `tokens.json` → `checks.primary_steps` also lists each step's contrast as text on the page.

**CSS variables:**
- `--aiplex-primary-{step}` follows the theme.
- `--aiplex-primary-light-{step}` and `--aiplex-primary-dark-{step}` are fixed.

**Semantic aliases.** A product can theme itself from these alone. Each switches with `prefers-color-scheme` and with `data-theme="light|dark"`.

| Alias | Light | Dark |
|---|---|---|
| `--aiplex-color-bg` | `#FFFFFF` | `#0B0D13` |
| `--aiplex-color-surface` | `#F6F7F9` | `#12151D` |
| `--aiplex-color-surface-raised` | `#FFFFFF` | `#191C26` |
| `--aiplex-color-border` | `#D8DCE4` | `#343947` |
| `--aiplex-color-text` | `#141A2E` | `#E8EBF2` |
| `--aiplex-color-text-muted` | `#4A5163` | `#8890A3` |
| `--aiplex-color-primary` | `#2F4FD8` | `#8DA2FF` |
| `--aiplex-color-primary-hover` | `#2541BB` | `#A8BAFE` |
| `--aiplex-color-primary-contrast` (text on primary) | `#FFFFFF` (6.50; 8.23 on hover) | `#0B0D13` (8.09; 10.28 on hover) |
| `--aiplex-color-primary-subtle` | `#E5EDFE` | `#171C2F` |
| `--aiplex-color-focus-ring` | `#2F4FD8` | `#8DA2FF` |
| `--aiplex-color-status-{ok,warning,denied,unknown}-{fg,bg}` | as in the status table | as in the status table |

The earlier names still work: `--aiplex-gate`, `--aiplex-text`, `--aiplex-status-ok`, `--aiplex-module-*`, `--aiplex-brand-*` and the rest.

## Typography

- **Wordmark:** custom outlines (above). It has no font dependency.
- **Interface and documentation:** **Inter**, SIL Open Font License 1.1. It has a large x-height, tabular figures (`font-variant-numeric: tabular-nums` for usage tables) and a slashed-zero option. **Inter Display** (same family, same licence) is used for headlines of 30 px and larger in the composed graphics.
- **Code, scopes, IDs, hex values and commands:** **JetBrains Mono**, SIL Open Font License 1.1.
- **Font stacks** are in `tokens.css` (`--aiplex-font-sans`, `--aiplex-font-mono`). The tokens contain no type, spacing or radius scale. The Console and docs site already have scales, and these brand assets give no basis for choosing new values.
- **Outlined text:** no file under `brand/` contains `<text>`. Text in the brand sheets, social card, banner, launch graphics and architecture diagrams is drawn as glyph outlines from Inter and JetBrains Mono by `scripts/outline_text.py`, with pair kerning, so it renders the same with no fonts installed. This includes GitHub's sandboxed rendering of README images. The OFL permits outlines in artwork.

## Icons

`icons/` holds 12 glyphs on a shared 24 × 24 grid:

- 2-unit stroke, butt caps, mitre joins, square corners, 90° and 45° angles only. Circles appear only where the subject needs one (user, gauge).
- Live area 2–22. Lines sit on whole coordinates so they are crisp at 24 px.
- Where the subject has a governed point, it is drawn as a solid square: the gate from the mark.
- Every file uses `currentColor`. Set `color` to Ink or a module colour.

| File | Meaning |
|---|---|
| `mcplex.svg` | Tools over MCP (plug) |
| `a2aplex.svg` | Agent to agent (two agents, one lane, a gate) |
| `llmplex.svg` | Models (processor package) |
| `quotaplex.svg` | Budgets and limits (gauge) |
| `clientplex.svg` | Developer clients (terminal) |
| `skillsplex.svg` | Skills (catalogue of tiles) |
| `access-point.svg` | Enforcement point per audience (posts and gate) |
| `identity.svg` | Identity (credential card) |
| `usage.svg` | Usage ledger (columns) |
| `agent.svg`, `user.svg`, `client.svg` | Caller kinds |

## Do and don't

Do:
- Use the colour files on white or neutral-50.
- Use the `-reversed` files on dark surfaces.
- Use the `-mono` files where only one colour is available.
- Keep clear space G.
- Pair a module colour with the module's icon and name, everywhere: navigation, diagrams, banners, launch graphics.

Don't:
- Put the colour files on dark backgrounds (the Ink lanes disappear).
- Recolour the lanes, or use module or status colours in the mark.
- Stretch, rotate, outline or re-space the mark or wordmark.
- Round the gate.
- Add shadows, gradients or glows.
- Retype "AIPlex" in a font, or write it as "AiPlex" or "AI Plex".
- Hard-code a documentation domain in an image; it may change.

## Integration notes

| Use | File |
|---|---|
| Docs navbar logo, light theme | `logo/aiplex-lockup-horizontal.svg` |
| Docs navbar logo, dark theme | `logo/aiplex-lockup-horizontal-reversed.svg` |
| Favicon (docs and Console) | `logo/aiplex-favicon.svg`. It switches with `prefers-color-scheme`; add an `.ico`/PNG fallback if needed. |
| Console header | `logo/aiplex-lockup-horizontal-mono.svg` inlined so it takes the header's text colour, or the light/reversed pair |
| Social card (`og:image`) | `social/aiplex-social-card.svg`. Platforms need a raster: use `.renders/export/aiplex-social-card.png` (1200 × 630). |
| README banner | `social/aiplex-readme-banner.svg`. It has its own Ink panel, so it reads on light and dark pages. |
| Brand sheet | `brand-sheet.svg` on light pages, `brand-sheet-dark.svg` on dark pages (same content). |
| Architecture diagram (README, docs) | `launch/architecture.svg` and `launch/architecture-dark.svg`. In Markdown, use `<picture>` with `prefers-color-scheme` sources to swap them. |
| App or PWA icon | `logo/aiplex-app-icon.svg` (512 base) |

## Launch graphics

All files are in `launch/`. Upload the PNGs, which `node brand/scripts/render.js export` writes to `brand/.renders/export/`.

| File | Size | Use |
|---|---|---|
| `aiplex-linkedin-light.svg`, `aiplex-linkedin-dark.svg` | 1200 × 627 | Link preview or single-image post |
| `aiplex-square-light.svg`, `aiplex-square-dark.svg` | 1080 × 1080 | Square post |
| `aiplex-carousel-1.svg` … `aiplex-carousel-5.svg` | 1080 × 1080 each | Carousel: the problem, one URL per audience, who used what, one identity across clouds, try it |
| `architecture.svg`, `architecture-dark.svg` | 1200 × 660 | README and docs diagram |

- **Claims:** the text follows the documentation (access points, usage ledger, agent identity federation). It contains no performance numbers or customer names. The usage-ledger frame shows bars, not invented figures.
- **Legibility:** every image was read at 400 px wide (`.renders/preview-400.png`). The smallest text there is about 10 px.
- **The command in frame 5:** this is one self-contained `<path id="launch-command">` in `aiplex-carousel-5.svg`; the command it shows is `aiplex dev`. To change it, regenerate the line and replace that element, or change the string in `f_try` in `build_sheets.py` and rebuild:

  ```bash
  python3 brand/scripts/outline_text.py --font mono-500 --size 48 --x 176 --y 602 \
      --fill "#E8EBF2" --id launch-command "<new command>"
  ```

  The terminal panel is 920 px wide. At 48 px JetBrains Mono, a command of up to 27 characters fits on one line (28.8 px per character, 784 px available).

## Scripts

All scripts use only the standard library or the Playwright copy that is already installed. Inter and JetBrains Mono must be installed only to regenerate outlined text.

```bash
python3 brand/scripts/validate.py          # XML, SVG hygiene and no <text>, for every file under brand/
python3 brand/scripts/palette.py           # contrast, colour-vision and tonal-scale checks (exit 1 on failure)
python3 brand/scripts/palette.py --write   # regenerate tokens/tokens.css and tokens/tokens.json
python3 brand/scripts/build_sheets.py      # rebuild brand sheets, social, launch, explorations (checks text contrast)
python3 brand/scripts/outline_text.py ...  # one line of text as a self-contained outlined <path>
node brand/scripts/render.js               # renders to brand/.renders/ + layout and font-independence checks
node brand/scripts/render.js export        # upload-ready PNGs to brand/.renders/export/ + 400 px previews
```

- **What `render.js` renders:** each logo at 16, 24, 32, 64 and 256 px, on light and dark backgrounds, under both `prefers-color-scheme` values, with an 8× nearest-neighbour enlargement of the real 16 and 24 px rasters. The icon family together at 16, 24 and 48 px. Every composed file at 1:1.
- **What `render.js` checks:** that each outlined text run sits inside the viewBox and does not overlap another run. It then renders every composed file, logo and icon again in a Chromium with an empty fontconfig, with no fonts at all, and requires pixel-identical output. A live-text control has to come out different, which proves the fonts were really absent.
- **What is hand-drawn:** the mark, wordmark and icons. `build_sheets.py` only arranges those drawings with outlined text.

## Files

```
brand/
  README.md                 this guide
  brand-sheet.svg           one-page overview (light)
  brand-sheet-dark.svg      the same on the dark palette
  logo/                     mark, wordmark, lockups (colour, -mono, -reversed), favicon, app icon
  icons/                    12 module and caller glyphs, 24 × 24, currentColor
  tokens/tokens.css         CSS custom properties, light + dark (prefers-color-scheme and data-theme)
  tokens/tokens.json        same values, plus contrast, tonal-scale and colour-vision results
  social/                   social card 1200 × 630, README banner 1280 × 320
  launch/                   LinkedIn 1200 × 627 and square 1080 × 1080 (light, dark), carousel 1–5,
                            architecture diagram (light, dark)
  explorations/             two alternative marks and a comparison sheet; not in use (see its README)
  scripts/                  validate.py, palette.py, build_sheets.py, outline_text.py, render.js
```
