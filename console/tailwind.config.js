/** @type {import('tailwindcss').Config} */
export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        brand: {
          // Defined in src/index.css from the brand tokens (src/brand/tokens.css).
          50: 'var(--console-brand-50)',
          300: 'var(--console-brand-300)',
          500: 'var(--console-brand-500)',
          600: 'var(--console-brand-600)',
          700: 'var(--console-brand-700)',
        },
      },
    },
  },
  plugins: [],
}
