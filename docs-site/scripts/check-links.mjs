// Checks internal links, assets and #anchors in the built site (docs-site/build).
//   npm run build && npm run check:links
// External (http/https) links are not fetched.
import {promises as fs} from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const build = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', 'build');
const BASE = '/aiplex/';

async function walk(dir) {
  const out = [];
  for (const e of await fs.readdir(dir, {withFileTypes: true})) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) out.push(...(await walk(p)));
    else out.push(p);
  }
  return out;
}

async function exists(p) {
  try {
    return (await fs.stat(p)).isFile();
  } catch {
    return false;
  }
}

async function resolve(urlPath) {
  const rel = decodeURIComponent(urlPath.slice(BASE.length));
  for (const c of [rel, `${rel}.html`, path.join(rel, 'index.html')]) {
    const p = path.join(build, c);
    if (c !== '' && (await exists(p))) return p;
  }
  if (rel === '' && (await exists(path.join(build, 'index.html')))) return path.join(build, 'index.html');
  return null;
}

const files = await walk(build);
const pages = files.filter((f) => f.endsWith('.html'));
const idCache = new Map();
async function ids(file) {
  if (!idCache.has(file)) {
    const html = await fs.readFile(file, 'utf8');
    idCache.set(file, new Set([...html.matchAll(/\sid="([^"]+)"/g)].map((m) => m[1])));
  }
  return idCache.get(file);
}

let checked = 0;
const problems = [];
for (const page of pages) {
  const html = await fs.readFile(page, 'utf8');
  const pageUrl = BASE + path.relative(build, page).split(path.sep).join('/');
  for (const m of html.matchAll(/\s(?:href|src)="([^"]+)"/g)) {
    const raw = m[1].replace(/&amp;/g, '&');
    if (/^(https?:|mailto:|tel:|data:|javascript:)/i.test(raw)) continue;
    let url;
    try {
      url = new URL(raw, `http://x${pageUrl}`);
    } catch {
      problems.push(`${pageUrl}: unparsable ${raw}`);
      continue;
    }
    if (!url.pathname.startsWith(BASE)) {
      problems.push(`${pageUrl}: link outside base path: ${raw}`);
      continue;
    }
    checked++;
    const target = await resolve(url.pathname);
    if (!target) {
      problems.push(`${pageUrl}: missing target ${raw}`);
      continue;
    }
    if (url.hash.length > 1 && target.endsWith('.html')) {
      const id = decodeURIComponent(url.hash.slice(1));
      if (!(await ids(target)).has(id)) problems.push(`${pageUrl}: missing anchor ${raw}`);
    }
  }
}
console.log(`check-links: ${pages.length} pages, ${checked} internal references checked`);
if (problems.length) {
  console.error([...new Set(problems)].join('\n'));
  process.exit(1);
}
