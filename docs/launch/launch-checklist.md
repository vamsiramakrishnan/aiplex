# Launch checklist

Work through this in order before posting. Items marked **owner** need your GitHub account, your decisions or a machine this repository's tooling cannot reach. The announcement drafts are in [linkedin-post.md](linkedin-post.md).

## Before posting

### Repository

- [ ] **owner** Make the repository public, or confirm it is. Open it in a private window to be sure a stranger can see it.
- [ ] **owner** Fill in the contact in [`CODE_OF_CONDUCT.md`](../../CODE_OF_CONDUCT.md) (search for `TODO (repository owner)`). No address has been invented.
- [ ] **owner** Turn on GitHub's private vulnerability reporting (Settings, Code security) so the route in [`SECURITY.md`](../../SECURITY.md) and the issue-template link work. Try "Report a vulnerability" from the Security tab.
- [ ] **owner** Check the repository description, topics and social preview image. The social card PNG is `brand/.renders/export/aiplex-social-card.png` after the export below.
- [ ] Skim `git log` and the working tree for anything you would not want public (internal hostnames, project IDs, keys, personal paths). `design/` and `docs/superpowers/` hold working notes; decide whether to keep them.
- [ ] Confirm CI is green on `main`, including the new jobs: docs site, release config and examples.

### First release, so `install.sh` works

- [ ] **owner** Update `CHANGELOG.md`: rename `[Unreleased]` to `[0.1.0] - YYYY-MM-DD`, add an empty `[Unreleased]` above it, and update the link lines at the bottom. The release workflow takes the release notes from that section and fails if it is missing.
- [ ] **owner** Tag and push (the exact commands are in the report that accompanied these files; in short: `git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0`).
- [ ] Watch the Release workflow. Check that the GitHub release has six archives (`aiplex_0.1.0_{linux,darwin,windows}_{amd64,arm64}`), `checksums.txt` and the notes.
- [ ] On a clean machine: `curl -fsSL https://raw.githubusercontent.com/vamsiramakrishnan/aiplex/main/install.sh | bash`, then `aiplex version` and `aiplex dev`. The installer resolves the latest release through the GitHub API, which only works once a release exists.
- [ ] Add the install one-liner to the README quickstart if you want it there; until a release has been installed this way on a real machine, the README only lists the source build.

### Owner actions from the gateway work

- [ ] **owner** Create the `vamsiramakrishnan/homebrew-tap` repository (with a `Casks` directory) and add the `HOMEBREW_TAP_TOKEN` secret. Until both exist, the release workflow skips the cask. Check the cask on a Mac after the first tag.
- [ ] **owner** After the first image push, make the GHCR packages `aiplex-api` and `aiplex` public, then pull them anonymously to check.
- [ ] **owner** Decide on the macOS quarantine hook. Nothing in `install.sh` clears `com.apple.quarantine`; the cask's caveats in `.goreleaser.yaml` tell users to remove it by hand. Choose between that, a `postflight` step in the cask, or a note in the install section.
- [ ] **owner** Replace the OpenAI key that has no credit, then run the OpenAI and Anthropic first-party validation (send a chat, a streamed chat, a Responses call and an Anthropic Messages call through `aiplex dev --provider`, and compare the ledger with the provider's usage fields). Record the result in [model providers](../model-providers.md#what-was-run-against-live-providers).
- [ ] **owner** First GCP install: run `aiplex platform preflight` against the target project and fix each missing role it names before `aiplex platform apply`. Nothing in this repository has been applied to a live project.

### Documentation site

- [ ] **owner** Enable GitHub Pages: Settings, Pages, Source "GitHub Actions". Run the "Deploy Docs to GitHub Pages" workflow (it also runs on pushes that change `docs-site/`, `docs/` or `brand/`).
- [ ] Confirm the address. The site is configured for `https://vamsiramakrishnan.github.io/aiplex/` (`DOCS_ORIGIN` and `DOCS_BASE_URL` in `docs-site/docusaurus.config.ts`). Open it, then follow the README's three documentation links and the "Run it locally in a minute" page. If you use a custom domain instead, change those two constants and the link block at the bottom of `README.md`, and add `docs-site/static/CNAME`.
- [ ] Check the dark theme on the Brand page shows the dark sheet.

### Launch graphics

- [ ] Export the PNGs: `node brand/scripts/render.js export` (needs Playwright; writes `brand/.renders/export/`). Check each at phone size. Frame 5 shows `aiplex dev` and "Apache 2.0", which matches `LICENSE`.
- [ ] Decide which post draft to use, and add alt text to each image (the table in `linkedin-post.md` has it for the carousel).

### README on github.com

- [ ] Open the README on github.com in light mode and in dark mode (GitHub: profile Settings, Appearance). The banner should read on both, and the architecture diagram should switch between `architecture.svg` and `architecture-dark.svg`.
- [ ] Click every link in the README, `CONTRIBUTING.md`, `SECURITY.md` and `CODE_OF_CONDUCT.md` from github.com, including the three documentation-site links.

### Try the quickstart from scratch

Do this on a machine that has never built the project, with no `bin/` directory and no `~/.aiplex`.

- [ ] Clean Linux: follow the README quickstart exactly, copying each command. Compare the output with the README. Note the time `make build` takes the first time.
- [ ] Clean macOS (Intel or Apple silicon): the same. Nothing here was run on macOS by the people who wrote the docs; this is the first test.
- [ ] Run `make demo` and `make examples-test` on each.
- [ ] Check what happens if port 8080 is taken (`aiplex dev --port 18080` works).
- [ ] Optional, and use your own key: `export OPENAI_API_KEY=...` then `aiplex dev --provider openai --api-key-env OPENAI_API_KEY`, send one request, and look at `aiplex usage records`. This path has not been run against a live provider by the project. If it works, say so in the post; if it does not, fix or remove the sentence in the README.

### Last checks

- [ ] If the CLI's output changed since the README was written (the CLI is being reworked), re-run the quickstart and update the quoted output in `README.md`, `docs-site/docs/getting-started/local-development.md` and the `examples/*/README.md` files.
- [ ] Search the repository for claims you cannot back: performance numbers, customer names, "production-ready", "enterprise". There should be none.
- [ ] If the post mentions OpenTelemetry, `aiplex connect --write`, `aiplex platform preflight` or the price catalogue, check that line against the "Limits" or "not validated" section of its guide ([observability](../observability.md), [harness onboarding](../harness-onboarding.md), [Google Cloud permissions](../gcp-permissions.md), [usage ledger](../usage-ledger.md#price-catalogue)) on the day you post.
- [ ] Have someone who has not seen the project try the quickstart while you watch, saying nothing.

## What to watch after posting

First hour and first day:

- GitHub **Issues** and **Discussions** (turn Discussions on if you want questions kept out of Issues). Answer the first few quickly and thank people who report build problems.
- The **Security** tab for private reports.
- **Actions**: the CI and Pages deploy still green after any hotfix.
- Where people get stuck. Expect: Go version too old, port 8080 in use, Windows, the Console not being built into the binary (`aiplex dev` says so and how to build it), someone exposing a development-mode server, someone pointing a real client at the mock.
- Stars and traffic are not the signal. The signals are: did anyone get through the quickstart, which step they stalled on, and what they tried next.

First week:

- Turn recurring questions into README or docs changes, not replies.
- Keep a list of "could not complete the quickstart" reports; each is a bug in the docs or the tool.
- Cut `v0.1.1` for fixes rather than editing a published release. Keep the `CHANGELOG.md` section honest about what changed.
- If someone runs it against a real provider and compares cost with an invoice, ask whether you may record the result in the docs; no such comparison exists yet.
