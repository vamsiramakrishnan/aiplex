#!/usr/bin/env node
// Render every brand SVG with Playwright/Chromium so the results can be inspected.
//
//   node brand/scripts/render.js [filter]
//
// Writes PNG contact sheets to brand/.renders/ (git-ignored):
//   logo-<name>.png   each logo file at 16, 24, 32, 64 and 256 px high, light and dark backgrounds
//   icons.png         the module/caller icon family together at 16, 24 and 48 px, light and dark
//   sheet-<name>.png  brand sheet, social card and banner at 1:1
//   social-card.png   1200x630 raster of the social card, for og:image use
// For sheet-type files it also checks that every <text> element lies inside the
// viewBox and that no two <text> boxes overlap; problems are printed and the
// exit status is non-zero.
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BRAND = path.resolve(__dirname, '..');
const OUT = path.join(BRAND, '.renders');
const filter = process.argv[2] || '';
const BG = { light: { bg: '#FFFFFF', fg: '#141A2E' }, dark: { bg: '#0B0D13', fg: '#E8EBF2' } };
const uri = (svg) => 'data:image/svg+xml;base64,' + Buffer.from(svg).toString('base64');
const read = (p) => fs.readFileSync(p, 'utf8');
const composed = () => [...fs.readdirSync(BRAND).filter((f) => /^brand-sheet.*\.svg$/.test(f)).map((f) => path.join(BRAND, f)),
  ...['social', 'launch'].filter((d) => fs.existsSync(path.join(BRAND, d))).flatMap((d) => list(d))];
const list = (dir) => fs.readdirSync(path.join(BRAND, dir)).filter((f) => f.endsWith('.svg')).sort()
  .map((f) => path.join(BRAND, dir, f));

// <img> does not inherit CSS color, so currentColor files are inlined instead.
function asImg(file, h, fg) {
  const svg = read(file);
  if (svg.includes('currentColor')) {
    return `<span style="color:${fg};display:inline-block;height:${h}px;line-height:0">` +
      svg.replace(/<svg /, `<svg style="height:${h}px;width:auto" `) + '</span>';
  }
  return `<img src="${uri(svg)}" style="height:${h}px;width:auto">`;
}

async function shot(page, html, out, width = 1400) {
  await page.setViewportSize({ width, height: 200 });
  await page.setContent(`<!doctype html><body style="margin:0;font:12px monospace">${html}</body>`);
  await page.waitForTimeout(100);
  await page.screenshot({ path: out, fullPage: true });
}

(async () => {
  fs.mkdirSync(OUT, { recursive: true });
  const browser = await chromium.launch();
  const page = await browser.newPage();
  let problems = 0;

  // 1. logos
  for (const file of list('logo')) {
    if (filter && !file.includes(filter)) continue;
    const name = path.basename(file, '.svg');
    // one screenshot per mode so prefers-color-scheme inside the SVG (favicon) is exercised too
    for (const [mode, c] of Object.entries(BG)) {
      await page.emulateMedia({ colorScheme: mode });
      const html = `<div style="background:${c.bg};color:${c.fg};padding:12px;display:flex;gap:20px;align-items:flex-end;flex-wrap:wrap">` +
        [16, 24, 32, 64, 256].map((h) => asImg(file, h, c.fg)).join('') +
        `<span>${name} / ${mode} background, prefers-color-scheme: ${mode}</span></div>`;
      await shot(page, html, path.join(OUT, `logo-${name}-${mode}.png`));
      // the actual 16 px and 24 px rasters, enlarged 8x with nearest-neighbour so pixels can be inspected
      const els = page.locator('div > img, div > span > svg');
      const zoom = [];
      for (const i of [0, 1]) {
        const buf = await els.nth(i).screenshot();
        zoom.push(`<img src="data:image/png;base64,${buf.toString('base64')}" style="zoom:8;image-rendering:pixelated">`);
      }
      await shot(page, `<div style="background:${c.bg};padding:8px;display:flex;gap:24px;align-items:flex-end">${zoom.join('')}</div>`,
        path.join(OUT, `logo-${name}-${mode}-pixels.png`));
    }
    console.log('rendered', path.relative(BRAND, file));
  }

  // 2. icon family
  if (!filter || 'icons'.includes(filter)) {
    const icons = list('icons');
    let html = '';
    for (const [mode, c] of Object.entries(BG)) {
      for (const h of [16, 24, 48]) {
        html += `<div style="background:${c.bg};color:${c.fg};padding:10px 12px;display:flex;gap:${h}px;align-items:center">` +
          icons.map((f) => asImg(f, h, c.fg)).join('') + `<span>${h}px ${mode}</span></div>`;
      }
    }
    html += `<div style="padding:8px">${icons.map((f) => path.basename(f, '.svg')).join(' · ')}</div>`;
    await shot(page, html, path.join(OUT, 'icons.png'), 1300);
    console.log('rendered icons/ (' + icons.length + ')');
  }

  // 3. composed sheets: render inline at 1:1 and check the outlined text runs (aria-label groups)
  const sheets = composed();
  for (const file of sheets) {
    if (filter && !file.includes(filter)) continue;
    const name = path.basename(file, '.svg');
    const svg = read(file);
    const vb = svg.match(/viewBox="([^"]+)"/)[1].split(/\s+/).map(Number);
    for (const mode of ['light', 'dark']) {
      await page.emulateMedia({ colorScheme: mode });
      await page.setViewportSize({ width: vb[2], height: vb[3] });
      await page.setContent(`<!doctype html><body style="margin:0;background:${BG[mode].bg}">${svg}</body>`);
      await page.waitForTimeout(150);
      await page.screenshot({ path: path.join(OUT, `sheet-${name}-${mode}.png`), fullPage: true });
      if (name === 'aiplex-social-card' && mode === 'light') {
        await page.screenshot({ path: path.join(OUT, 'social-card.png'), clip: { x: 0, y: 0, width: 1200, height: 630 } });
      }
    }
    const issues = await page.evaluate(([x0, y0, w, h]) => {
      const out = [];
      const svgEl = document.querySelector('svg');
      const texts = [...svgEl.querySelectorAll('text, g[aria-label], path[aria-label]')];
      const boxes = texts.map((t) => {
        const b = t.getBBox();
        const m = t.getCTM();
        const pts = [[b.x, b.y], [b.x + b.width, b.y + b.height]].map(([px, py]) => {
          const p = svgEl.createSVGPoint(); p.x = px; p.y = py; return p.matrixTransform(m);
        });
        // CTM is in viewport (CSS px) space; convert back to viewBox units
        const s = w / svgEl.getBoundingClientRect().width;
        return { t: (t.getAttribute('aria-label') || t.textContent).trim().slice(0, 40), x1: pts[0].x * s + x0, y1: pts[0].y * s + y0, x2: pts[1].x * s + x0, y2: pts[1].y * s + y0 };
      });
      for (const b of boxes) {
        if (b.x1 < x0 - 0.5 || b.y1 < y0 - 0.5 || b.x2 > x0 + w + 0.5 || b.y2 > y0 + h + 0.5) out.push(`outside viewBox: "${b.t}"`);
      }
      for (let i = 0; i < boxes.length; i++) for (let j = i + 1; j < boxes.length; j++) {
        const a = boxes[i], b = boxes[j];
        const ix = Math.min(a.x2, b.x2) - Math.max(a.x1, b.x1), iy = Math.min(a.y2, b.y2) - Math.max(a.y1, b.y1);
        if (ix > 1 && iy > 1) out.push(`overlap: "${a.t}" / "${b.t}"`);
      }
      return { n: boxes.length, out };
    }, vb);
    console.log(`checked ${path.relative(BRAND, file)}: ${issues.n} text runs, ${issues.out.length} problems`);
    for (const i of issues.out) { console.log('  - ' + i); problems++; }
  }

  // 4. font independence: render every composed file in a second Chromium that has no fonts at all
  //    (empty fontconfig) and require pixel-identical output. A live-text control must differ.
  if (!filter || filter === 'fonts') {
    const conf = path.join(OUT, 'no-fonts.conf');
    fs.writeFileSync(conf, '<?xml version="1.0"?><!DOCTYPE fontconfig SYSTEM "fonts.dtd"><fontconfig>' +
      `<cachedir>${path.join(OUT, 'fc-cache')}</cachedir></fontconfig>`);
    const bare = await chromium.launch({ env: { ...process.env, FONTCONFIG_FILE: conf } });
    const bp = await bare.newPage();
    const raster = async (pg, svg) => {
      const vb = svg.match(/viewBox="([^"]+)"/)[1].split(/\s+/).map(Number);
      const w = Math.min(vb[2], 1600), h = Math.round(w * vb[3] / vb[2]);
      await pg.setViewportSize({ width: w, height: h });
      await pg.setContent(`<body style="margin:0"><img style="display:block;width:${w}px" src="${uri(svg)}"></body>`);
      await pg.waitForTimeout(150);
      return pg.screenshot();
    };
    const control = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 300 60"><rect width="300" height="60" fill="#fff"/>' +
      '<text x="10" y="40" font-family="Inter" font-size="32">AIPlex</text></svg>';
    const ctrlSame = Buffer.compare(await raster(page, control), await raster(bp, control)) === 0;
    console.log(`no-font browser: live-text control ${ctrlSame ? 'IDENTICAL (fonts were not removed)' : 'differs, as expected'}`);
    if (ctrlSame) problems++;
    let n = 0;
    for (const file of [...composed(), ...list('logo'), ...list('icons')]) {
      const svg = read(file);
      const same = Buffer.compare(await raster(page, svg), await raster(bp, svg)) === 0;
      n++;
      if (!same) { problems++; console.log('  FONT-DEPENDENT: ' + path.relative(BRAND, file)); }
    }
    console.log(`font independence: ${n} files rendered with and without fonts`);
    await bare.close();
  }

  await browser.close();
  console.log('PNG output in', path.relative(process.cwd(), OUT) || OUT);
  process.exit(problems ? 1 : 0);
})();
