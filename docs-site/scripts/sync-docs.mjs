// Copies the verified operator docs from <repo>/docs into docs-site/docs/library
// so Docusaurus can render them. docs/ stays the only place they are edited.
//
// - Only front matter is added (custom_edit_url, so "Edit this page" opens the
//   real source file). The body is copied byte for byte.
// - Links into repository source files are rewritten at build time by
//   plugins/remark-repo-links.mjs, not here.
//
// Run automatically by `npm run build` / `npm start` (pre-hooks).
import {promises as fs} from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const siteRoot = path.resolve(here, '..');
const srcRoot = path.resolve(siteRoot, '..', 'docs');
const dstRoot = path.join(siteRoot, 'docs', 'library');
const REPO_EDIT = 'https://github.com/vamsiramakrishnan/aiplex/edit/main/docs';

async function walk(dir, rel = '') {
  const out = [];
  for (const e of await fs.readdir(dir, {withFileTypes: true})) {
    const r = rel ? `${rel}/${e.name}` : e.name;
    if (e.isDirectory()) out.push(...(await walk(path.join(dir, e.name), r)));
    else if (e.name.endsWith('.md')) out.push(r);
  }
  return out;
}

await fs.rm(dstRoot, {recursive: true, force: true});
const files = await walk(srcRoot);
for (const rel of files) {
  const body = await fs.readFile(path.join(srcRoot, rel), 'utf8');
  if (body.startsWith('---\n')) {
    throw new Error(`docs/${rel} has front matter; extend sync-docs.mjs to merge it`);
  }
  const out = `---\ncustom_edit_url: ${REPO_EDIT}/${rel}\n---\n\n${body}`;
  const dst = path.join(dstRoot, rel);
  await fs.mkdir(path.dirname(dst), {recursive: true});
  await fs.writeFile(dst, out);
}
console.log(`sync-docs: ${files.length} files from docs/ -> docs-site/docs/library`);
