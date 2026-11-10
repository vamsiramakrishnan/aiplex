# AIPlex documentation site

A [Docusaurus](https://docusaurus.io/) site, deployed to GitHub Pages by `.github/workflows/deploy-docs.yml`.

```bash
cd docs-site
npm ci
npm start        # dev server at http://localhost:3000/aiplex/
npm run build    # production build into build/
npm run serve    # serve the build
npm run typecheck
```

## Where content lives

| Content | Location | Edited by hand? |
| --- | --- | --- |
| Overview, getting started, concepts, guides, operations, API overview | `docs-site/docs/` | Yes |
| Verified feature docs (access points, usage ledger, identity, QuotaPlex, model providers, ...) | `../docs/*.md`, rendered under `/docs/library/` | Yes, in `docs/`, never here |
| CLI reference | `docs-site/docs/reference/cli/` | No, generated (see below) |
| Landing page | `src/pages/index.tsx` | Yes |

### Single source for `docs/`

`docs/*.md` in the repository root is the only copy of the verified docs. `npm run build` and `npm start` first run `scripts/sync-docs.mjs` (the `prebuild` and `prestart` hooks), which copies `../docs` to `docs-site/docs/library/` (git-ignored) and adds one front-matter field, `custom_edit_url`, so "Edit this page" opens the real file. The body is copied unchanged. `docs/superpowers/` and `docs/integration/` are not published.

Relative links between those docs work as written. Links that leave `docs/` (for example `../internal/api/x.go` or `../pkg/quotaplex`) are rewritten to the GitHub repository (`blob` for files, `tree` for directories) by `plugins/remark-repo-links.mjs`, a remark plugin registered in `docusaurus.config.ts`. The build fails if a link points at a path that does not exist in the repository.

The config sets `markdown.format: 'detect'`: `.md` files are parsed as plain CommonMark and `.mdx` as MDX, so `<`, `{` and angle-bracket placeholders in the verified docs need no escaping. Use `.md` for new pages unless they need JSX.

To add a new `docs/*.md` page to the navigation, add `L('name', 'Label')` to the right category in `sidebars.ts`. The page is built either way.

### Generated CLI reference

```bash
npm run gen:cli
```

`scripts/gen-cli-reference.mjs` builds `cmd/aiplex-cli` into a temporary directory (Go is required), then walks `--help` recursively and writes `docs/reference/cli/index.md` and one page per top-level command. The output is committed, so the site builds without Go. Regenerate and commit it whenever a command, flag or help text changes.

## Link and quality checks

The build fails on broken links, broken anchors and broken Markdown links/images (`onBrokenLinks`, `onBrokenAnchors` and the `markdown.hooks` are all `throw`). `npm run typecheck` runs `tsc --noEmit`. After a build, `npm run check:links` crawls `build/` and verifies every internal link, asset and `#anchor`.

## Brand assets

`brand/` in the repository root is the only place brand assets are edited. `scripts/sync-brand.mjs` (run by `npm run sync`, so by `npm run build` and `npm start`) copies them into the site. The copies are git-ignored and regenerated on every build, so they cannot drift, and they work in CI because the job checks out the whole repository.

| Source | Copied to | Used for |
| --- | --- | --- |
| `brand/logo/aiplex-lockup-horizontal.svg` | `static/img/aiplex-logo.svg` | Navbar logo (light) |
| `brand/logo/aiplex-lockup-horizontal-reversed.svg` | `static/img/aiplex-logo-dark.svg` | Navbar logo (dark) |
| `brand/logo/aiplex-favicon.svg` | `static/img/favicon.svg` | Favicon |
| `brand/tokens/tokens.css` | `src/css/brand-tokens.css` | Imported by `src/css/custom.css`; Infima variables map onto it |
| `brand/icons/*.svg` | `src/brand/icons/` | Landing page icons (imported as React components so `currentColor` works) |
| `brand/logo`, `icons`, `social`, `brand-sheet.svg` | `static/img/brand/` | The Brand page |

### Social card

Social sites need a PNG and the docs CI job has no Playwright, so `static/img/aiplex-social-card.png` is committed. Regenerate it whenever `brand/social/aiplex-social-card.svg` changes:

```bash
node brand/scripts/render.js social                      # from the repository root; needs Playwright
cp brand/.renders/social-card.png docs-site/static/img/aiplex-social-card.png
```

The output is 1200 x 630.

## Deployment

On a push to `main` that changes `docs-site/**`, `docs/**`, `brand/**` or the workflow file, the workflow runs `npm ci && npm run build` in this directory and publishes `build/`.
