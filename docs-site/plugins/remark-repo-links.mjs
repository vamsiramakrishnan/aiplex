// Remark plugin: in the synced copies of docs/*.md, rewrite links that leave the
// docs/ tree (../internal/api/x.go, ../pkg/quotaplex, ../examples/...) and links to
// docs that are not published (e.g. docs/superpowers) to the GitHub repository.
// Links between published docs are left alone for Docusaurus to resolve.
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const siteRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const repoRoot = path.resolve(siteRoot, '..');
const libRoot = path.join(siteRoot, 'docs', 'library');
const REPO_URL = 'https://github.com/vamsiramakrishnan/aiplex';
const BRANCH = 'main';

function visit(node, fn) {
  fn(node);
  if (node.children) for (const c of node.children) visit(c, fn);
}

export default function remarkRepoLinks() {
  return (tree, file) => {
    const filePath = file.path;
    if (!filePath || !filePath.startsWith(libRoot + path.sep)) return;
    visit(tree, (node) => {
      if (node.type !== 'link' && node.type !== 'definition') return;
      const url = node.url;
      if (!url || /^([a-z][a-z0-9+.-]*:|#|\/)/i.test(url)) return;
      const [target, hash] = url.split('#');
      const abs = path.resolve(path.dirname(filePath), decodeURI(target));
      if (fs.existsSync(abs) && abs.startsWith(libRoot + path.sep) && fs.statSync(abs).isFile()) {
        return; // published doc: Docusaurus resolves it
      }
      // The synced tree mirrors docs/, so map the location back to the repository.
      const relToLib = path.relative(libRoot, abs);
      const repoPath = path.posix.normalize(
        path.posix.join('docs', relToLib.split(path.sep).join('/')),
      );
      const onDisk = path.join(repoRoot, repoPath);
      if (!fs.existsSync(onDisk)) {
        throw new Error(
          `${filePath}: link "${url}" points at ${repoPath}, which does not exist in the repository`,
        );
      }
      const kind = fs.statSync(onDisk).isDirectory() ? 'tree' : 'blob';
      node.url = `${REPO_URL}/${kind}/${BRANCH}/${repoPath}${hash ? `#${hash}` : ''}`;
    });
  };
}
