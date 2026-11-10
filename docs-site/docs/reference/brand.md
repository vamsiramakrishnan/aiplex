---
title: Brand
description: "The AIPlex identity: mark, lockups, colours, module icons and where the asset files live."
---

# Brand

![AIPlex brand sheet](/img/brand/brand-sheet.svg)

The identity is a single mark: three lanes (callers) converge into one square gate and leave as one lane. The assets live in the repository's [`brand/`](https://github.com/vamsiramakrishnan/aiplex/tree/main/brand) directory, which is the only place they are edited. This site copies them at build time.

Read the [brand guide](https://github.com/vamsiramakrishnan/aiplex/blob/main/brand/README.md) before using them. It covers construction, clear space (the gate width on every side), minimum sizes, which variant goes on which background, the contrast results and the colour-vision checks.

## Files

| Asset | Location |
| --- | --- |
| Mark, wordmark and lockups (colour, `-mono`, `-reversed`), favicon, app icon | [`brand/logo/`](https://github.com/vamsiramakrishnan/aiplex/tree/main/brand/logo) |
| Module and caller icons (24 px grid, `currentColor`) | [`brand/icons/`](https://github.com/vamsiramakrishnan/aiplex/tree/main/brand/icons) |
| CSS tokens (light and dark) and the same values as JSON | [`brand/tokens/`](https://github.com/vamsiramakrishnan/aiplex/tree/main/brand/tokens) |
| Social card and README banner | [`brand/social/`](https://github.com/vamsiramakrishnan/aiplex/tree/main/brand/social) |
| Validation, palette and render scripts | [`brand/scripts/`](https://github.com/vamsiramakrishnan/aiplex/tree/main/brand/scripts) |

## Rules that matter in the docs

- Use the colour lockup on light backgrounds and the `-reversed` lockup on dark ones; the navbar switches between them with the theme.
- Module colours (MCPlex, A2APlex, LLMPlex, QuotaPlex, ClientPlex, SkillsPlex) are for icons and diagrams. Always show the module icon and name next to the colour, and never use a module colour to mean status.
- Status colours (ok, warning, denied, unknown) are always paired with an icon or a label.

## Where this site uses them

| Use | Source file |
| --- | --- |
| Navbar logo, light | `brand/logo/aiplex-lockup-horizontal.svg` |
| Navbar logo, dark | `brand/logo/aiplex-lockup-horizontal-reversed.svg` |
| Favicon | `brand/logo/aiplex-favicon.svg` |
| Colours | `brand/tokens/tokens.css` |
| Landing-page icons | `brand/icons/*.svg` |
| Social card | `brand/social/aiplex-social-card.svg`, rendered once to `static/img/aiplex-social-card.png` |
