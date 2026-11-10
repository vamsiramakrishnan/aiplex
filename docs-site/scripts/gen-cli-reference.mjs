// Generates docs-site/docs/reference/cli/*.md from the real `aiplex` binary.
//
//   node scripts/gen-cli-reference.mjs        (or: npm run gen:cli)
//
// Builds cmd/aiplex-cli into a temporary directory, then walks `--help`
// recursively. One page per top-level command plus an index page. The output is
// committed so the site builds without Go; regenerate it when the CLI changes.
import {execFileSync} from 'node:child_process';
import {promises as fs} from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const siteRoot = path.resolve(here, '..');
const repoRoot = path.resolve(siteRoot, '..');
const outDir = path.join(siteRoot, 'docs', 'reference', 'cli');

const tmp = await fs.mkdtemp(path.join(os.tmpdir(), 'aiplex-cli-'));
const bin = path.join(tmp, 'aiplex');
execFileSync('go', ['build', '-o', bin, './cmd/aiplex-cli'], {cwd: repoRoot, stdio: 'inherit'});

// Isolated HOME and environment so help output never depends on local state.
const env = {PATH: process.env.PATH, HOME: tmp, NO_COLOR: '1', TERM: 'dumb'};

function help(args) {
  return execFileSync(bin, [...args, '--help'], {env, encoding: 'utf8'}).replace(/\s+$/, '') + '\n';
}

function subcommands(text) {
  const m = text.match(/^Available Commands:\n((?:  .*\n?)+)/m);
  if (!m) return [];
  return m[1]
    .split('\n')
    .map((l) => l.match(/^  (\S+)\s+(.*)$/))
    .filter(Boolean)
    .map((x) => ({name: x[1], short: x[2].trim()}))
    .filter((c) => c.name !== 'help' && c.name !== 'completion');
}

async function walk(args) {
  const text = help(args);
  const node = {args, text, children: []};
  for (const c of subcommands(text)) {
    node.children.push({short: c.short, ...(await walk([...args, c.name]))});
  }
  return node;
}

const root = await walk([]);
const top = subcommands(root.text);
const NOTE =
  'This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. ' +
  'Regenerate it with `npm run gen:cli` in `docs-site/`.';

function section(node, depth) {
  let md = `${'#'.repeat(depth)} \`aiplex ${node.args.join(' ')}\`\n\n\`\`\`text\n${node.text}\`\`\`\n\n`;
  for (const c of node.children) md += section(c, Math.min(depth + 1, 6));
  return md;
}

await fs.rm(outDir, {recursive: true, force: true});
await fs.mkdir(outDir, {recursive: true});

let idx = `---
title: CLI reference
sidebar_label: Overview
description: Every aiplex command, generated from the CLI's own help output.
---

${NOTE}

## Global flags

\`\`\`text
${(root.text.match(/^Flags:\n((?:  .*\n?)+)/m) || ['', ''])[1].replace(/\s+$/, '')}
\`\`\`

## Commands

| Command | Purpose |
| --- | --- |
`;
for (const c of top) idx += `| [\`aiplex ${c.name}\`](./${c.name}.md) | ${c.short} |\n`;
idx += `\n## Root help\n\n\`\`\`text\n${root.text}\`\`\`\n`;
await fs.writeFile(path.join(outDir, 'index.md'), idx);

for (const c of top) {
  const node = root.children.find((n) => n.args[0] === c.name);
  const page = `---
title: aiplex ${c.name}
sidebar_label: ${c.name}
description: ${JSON.stringify(c.short)}
---

${NOTE}

${section(node, 2).replace(/^## `aiplex [^\n]*`\n\n/, '')}`;
  await fs.writeFile(path.join(outDir, `${c.name}.md`), page);
}
await fs.rm(tmp, {recursive: true, force: true});
console.log(`gen-cli-reference: ${top.length} command pages + index in docs/reference/cli`);
