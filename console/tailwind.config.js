/** @type {import('tailwindcss').Config} */
const scale = [50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950]
const tone = (name) => ({ fg: `var(--aiplex-color-status-${name}-fg)`, bg: `var(--aiplex-color-status-${name}-bg)` })

export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // The brand scale is the token scale (src/brand/tokens.css, from brand/tokens).
        // The tokens flip it in dark mode, so brand-600 is the readable accent in both themes
        // and brand-contrast is the text colour that goes on it.
        brand: {
          ...Object.fromEntries(scale.map((step) => [step, `var(--aiplex-primary-${step})`])),
          contrast: 'var(--aiplex-color-primary-contrast)',
        },
        // Status foreground/background pairs: use text-status-ok-fg on bg-status-ok-bg, etc.
        status: {
          'ok-fg': tone('ok').fg, 'ok-bg': tone('ok').bg,
          'warning-fg': tone('warning').fg, 'warning-bg': tone('warning').bg,
          'denied-fg': tone('denied').fg, 'denied-bg': tone('denied').bg,
          'unknown-fg': tone('unknown').fg, 'unknown-bg': tone('unknown').bg,
        },
        // One accent per plane, for the dots and tags that tell the planes apart.
        module: Object.fromEntries(['mcplex', 'a2aplex', 'llmplex', 'quotaplex', 'clientplex', 'skillsplex'].map((m) => [m, `var(--aiplex-module-${m})`])),
      },
      ringColor: { DEFAULT: 'var(--aiplex-color-focus-ring)' },
    },
  },
  plugins: [],
}
