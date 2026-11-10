# AIPlex Console

React + Vite + TypeScript + Tailwind + react-query. The Console is a thin client of the AIPlex API: it asks
`GET /api/v1/iam/whoami` who you are and what roles you hold, and shows only what you can use.

## Build and test

```
npm ci
npx tsc --noEmit
npm test              # vitest, jsdom, mocked fetch
npm run build         # writes console/built (git-ignored)
npm run sync:brand    # copy brand tokens and logos from ../brand
```

`npm run build` writes to `console/built/`, which is ignored by git. `console/dist/index.html` is a tracked
placeholder that is never written; the Go embed (`console/embed.go`) serves `built/` when a build is present and
the placeholder otherwise. Run `make build-cli` after building the Console to embed it in `bin/aiplex`.

## Run it against a local API

The quickest way is `aiplex dev`: it serves the Console on the port it prints and signs you in automatically
(the Console proxy adds the developer token, and the Console accepts a server-provided session, so there is no
sign-in form). Against any other API, run `cd console && npm run dev` (proxies `/api` to `localhost:8080`) and paste
a bearer token on the sign-in page.

## What each caller sees

| Caller | Sees |
|---|---|
| admin | everything |
| deployer | configuration screens; admin-only navigation and actions (dashboard, permissions, access point edit, prices) are disabled with the reason |
| no role | a Home page: who they are, the access points that admit them with server-built endpoint URLs, and their own usage |

A 403 that still happens renders as "You need the admin role for this", never a raw error or a spinner.

## Browser checks (`console/e2e`)

Plain Node scripts that drive the built Console in Chromium against a real local API, using the pre-installed
Playwright library (`require('playwright')`; nothing is added to `package.json`). Each check is a regression test for
a defect that was fixed.

```
make build                                   # bin/aiplex, bin/aiplex-api (repository root)
cd console && npm ci && npm run build        # console/built
e2e/run.sh start /tmp/console-e2e            # local API + seeded data + Console on :19090; prints the harness file
node e2e/flows.mjs --harness /tmp/console-e2e/harness.json                # all flows
node e2e/flows.mjs --harness /tmp/console-e2e/harness.json --only usage,no-role
e2e/screens-all.sh /tmp/console-e2e/harness.json                          # screenshots + accessibility scan
e2e/run.sh stop /tmp/console-e2e
```

* `run.sh` starts `tools/consolee2e` (the same stack as `aiplex dev`, plus a no-role caller `bob` and a deployer
  `dana`, three model surfaces, five access points, an agent identity binding and a user identity provider),
  `serve.mjs` (static Console plus API proxy, no token injection) and generates traffic with `traffic.mjs`.
* `flows.mjs` sections: `access-points`, `usage`, `llmplex`, `connect`, `identity`, `mcplex`, `no-role`, `deployer`,
  `dashboard`, `keyboard-and-login`. Values on screen are compared with `aiplex usage records|summary -o json`.
* `screens.mjs` captures each route at 1440 and 390 px, light and dark, into `console/e2e/shots/<caller>/` and runs
  `a11yScan` (labels, names, landmarks, one `main`, contrast of every text node, horizontal overflow).
* `dump.mjs` and `api.mjs` are debugging aids (print a screen's controls; call the API as a caller).

Exit code is non-zero when a check fails. Set `AIPLEX_BIN` to use a different `aiplex` binary (default `bin/aiplex`).

## Status colours and the brand scale

Colours come from `src/brand/tokens.css` (copied from `brand/tokens`): `bg-brand-50..950` is the token tonal scale
(flips in dark mode), `text-brand-contrast` goes on `bg-brand-600`, and `text-status-{ok,warning,denied,unknown}-fg` /
`bg-status-*-bg` are the status pairs. Do not use raw `text-red-600`-style classes for status.
