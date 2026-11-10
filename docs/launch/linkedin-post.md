# LinkedIn announcement drafts

Three alternatives, all in the first person of the project owner. Pick one, check the facts against the repository on the day you post (see [launch-checklist.md](launch-checklist.md)), and replace the link placeholders if the repository address or documentation address differs.

Facts every draft relies on, and where they come from:

| Statement | Source |
| --- | --- |
| `aiplex dev` runs locally with a mock model and mock MCP server, needs Go 1.25+ and no cloud account or API key | `make demo` and `make examples-test` run it in CI; README quickstart |
| One URL per audience, checked before anything is reached | access points, [docs/access-points.md](../access-points.md) |
| A usage record per call: caller, model or tool, tokens, estimated cost; unknown stays unknown | [docs/usage-ledger.md](../usage-ledger.md); the README quickstart output |
| Tested against local mocks, recorded provider fixtures and the Firestore emulator | CI, [production qualification](../production-qualification.md) |
| Not run against live providers, not compared with an invoice, no recorded cloud deployment | README "Project status" |
| Pre-1.0 | README, CHANGELOG |

Avoid adding claims about speed, scale, customers or cost savings; none is measured or recorded.

---

## Draft 1: short (about 630 characters)

```text
I've open-sourced AIPlex: a gateway between the people, agents and dev tools that call AI models and MCP tools, and those models and tools.

One URL per audience. The caller's identity is checked before anything is reached. Every call gets a usage record: who, which model or tool, tokens, estimated cost.

Try it locally, no cloud account or API key (Go 1.25+):

git clone https://github.com/vamsiramakrishnan/aiplex && cd aiplex && make build && bin/aiplex dev

Pre-1.0. Tested against local mocks and recorded provider fixtures, not yet against live providers or an invoice. Feedback welcome, especially on the first five minutes.
```

## Draft 2: medium (about 1,400 characters)

```text
I've open-sourced AIPlex: a control plane for how agents, people and developer tools reach AI models, MCP tools and skills.

The problem I kept running into: every agent, IDE and script holds its own keys, and "who called what, as whom, at what cost" ends up spread across providers.

What AIPlex does about it:
- An access point gives a group of callers one stable URL in front of models, tools and skills, and checks who is calling before anything is reached.
- Every exchange goes into a usage ledger: caller, model or tool, tokens, estimated cost. A value that isn't known shows as unknown, never as zero.
- Agents can sign in with a token their own cloud issued (AWS, Azure, GitHub Actions) instead of a shared key.

To try it, with no cloud account or API key (Go 1.25+):

git clone https://github.com/vamsiramakrishnan/aiplex
cd aiplex && make build && bin/aiplex dev

That starts a local API with a mock model and a mock MCP server. Point an OpenAI SDK at it, then run `aiplex usage summary`.

Where it stands: pre-1.0. Tested against local mocks, recorded provider fixtures and the Firestore emulator. Not run against live providers, not compared with a provider invoice, not deployed to a live cloud project from this repository. The README says so too.

I'd like to hear what breaks or confuses you in the first five minutes.
https://github.com/vamsiramakrishnan/aiplex
```

## Draft 3: carousel caption (five frames, `brand/launch/aiplex-carousel-1.svg` to `-5.svg`)

Upload the PNGs from `node brand/scripts/render.js export` (`brand/.renders/export/`) in order. Post text:

```text
I've open-sourced AIPlex, a control plane for how agents, people and developer tools reach AI models, MCP tools and skills. Five frames below. The command in the last one runs locally, with no cloud account or API key.

Pre-1.0: tested against local mocks, recorded provider fixtures and the Firestore emulator; not yet against live providers or an invoice. Feedback welcome.

Code and docs: https://github.com/vamsiramakrishnan/aiplex
```

Per-frame alt text, matching each image (the frames carry their own headline and caption):

| Frame | Headline in the image | Alt text to add |
| --- | --- | --- |
| 1 of 5 | Agents reach everything on their own. | Every agent, IDE and script holds its own keys. Who called what, as whom and at what cost is spread across providers. |
| 2 of 5 | One URL per audience. | An access point puts models, tools and skills behind one stable URL and checks who the caller is before anything is reached. |
| 3 of 5 | Who used what, in tokens and money. | One append-only usage ledger records each exchange: caller, resource, reported tokens, and cost when priced. |
| 4 of 5 | One identity across clouds. | Workloads on AWS, Azure or GitHub Actions sign in as a registered agent with a token their own platform issued. |
| 5 of 5 | Try it in one command. | A terminal showing the command `aiplex dev`. Apache 2.0. Source and documentation on GitHub. |

Optional first comment (the command needs a built binary until the first release exists):

```text
Quickstart: git clone https://github.com/vamsiramakrishnan/aiplex && cd aiplex && make build && bin/aiplex dev
Then, in a second terminal: eval "$(bin/aiplex dev env)" and send a request; the README shows the exact commands and output.
```

Once a release exists and `install.sh` has been tried on the day, the comment can use the installer instead: `curl -fsSL https://raw.githubusercontent.com/vamsiramakrishnan/aiplex/main/install.sh | bash` and then `aiplex dev`.
