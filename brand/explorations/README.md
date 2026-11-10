# Mark explorations

These are not in use. The current mark (`../logo/aiplex-mark.svg`) is unchanged. The owner chooses.

The aim was a small, principled change that makes the mark less like a generic "merge" icon. It had to keep three things: the idea (three lanes, one governed point), legibility at 16 px, and a single-colour reading.

| File | Change | 16 px | One colour |
|---|---|---|---|
| `aiplex-mark-alt-a-split-gate.svg` | The lanes merge into one lane that passes *through* the gate, between two jaws. | Legible. Jaws are 2 px bars with 1 px gaps. | Reads as "≥ =": the solid governed point is lost. |
| `aiplex-mark-alt-b-checkpoint.svg` | The lanes merge first, then stop 2 units short of the gate, cut square. The gap is the check. One lane leaves. | Legible. Gate and gap fall on whole pixels; the gap is 1 px. | Two clear parts, a merged chevron and a solid block. The block still reads as the governed point. |

`aiplex-mark-explorations.svg` shows the current mark, A and B at real sizes (16, 32 and 128 px), in colour on light, reversed on dark and in one colour. To regenerate it, run `python3 brand/scripts/build_sheets.py`. To render, run `node brand/scripts/render.js explorations` for the sheet and `node brand/scripts/render.js alt-` for each exploration at 16 to 256 px, with 8× pixel zooms.

## Recommendation: B, if the owner wants a change

- **Why B is more ownable:** a merge icon joins lines at a node. B joins them into one lane before anything else happens. The separate solid gate then turns the shape into a sentence: traffic converges, is checked, then passes. It is the same geometry as the current mark (32 grid, 4-unit stroke, 45° turns, 12-unit gate) with one deliberate gap. All edges are on even units, so the 16 px raster stays crisp.
- **Cost:** the 1 px gap at 16 px can close up when a favicon is downsampled on a low-density screen, or when the browser draws the tab icon smaller than 16 px. The favicon would then look like the current mark, which is acceptable. A switch would also mean redrawing the favicon and app icon, and updating the lockups and the brand sheet.
- **A is not recommended:** splitting the gate into two thin jaws gives up the solid square. That square is the element the icon family reuses as the "gate".
