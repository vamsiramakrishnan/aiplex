// Copies the brand assets the Console uses from <repo>/brand into the Console.
//
//   npm run sync:brand
//
// The copies are committed because the Console's Docker build context is console/
// only and cannot see ../brand. src/__tests__/brand_sync.test.ts fails when a copy
// drifts from brand/, so run this after changing anything under brand/.
import {promises as fs} from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
export const COPIES = [
  ['tokens/tokens.css', 'src/brand/tokens.css'],
  ['logo/aiplex-lockup-horizontal.svg', 'src/brand/aiplex-lockup-horizontal.svg'],
  ['logo/aiplex-lockup-horizontal-reversed.svg', 'src/brand/aiplex-lockup-horizontal-reversed.svg'],
  ['logo/aiplex-favicon.svg', 'public/favicon.svg'],
];

for (const [from, to] of COPIES) {
  const dst = path.join(root, to);
  await fs.mkdir(path.dirname(dst), {recursive: true});
  await fs.copyFile(path.join(root, '..', 'brand', from), dst);
}
console.log(`sync-brand: ${COPIES.length} files copied from brand/`);
