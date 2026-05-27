import type { Config } from 'tailwindcss';

/**
 * Tailwind config — maps utilities onto the CSS-variable design tokens defined
 * in styles.css. Mintlify-derived palette: black-primary, mint accent, 6-level
 * gray text hierarchy, hairline borders.
 *
 * Backwards-compatible aliases (text-text-primary, bg-bg-app, etc.) are kept
 * so legacy components don't break — new components should prefer the canonical
 * names (text-ink, bg-canvas, border-hairline).
 */
const config: Config = {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  darkMode: 'class',
  theme: {
    container: {
      center: true,
      padding: '1rem',
    },
    extend: {
      fontFamily: {
        sans: ['"Plus Jakarta Sans"', 'ui-sans-serif', 'system-ui', '-apple-system', 'sans-serif'],
        mono: ['"Plus Jakarta Sans"', 'ui-sans-serif', 'system-ui', 'sans-serif'],
      },
      colors: {
        // ---- Canonical (Mintlify) ----
        canvas:        'rgb(var(--canvas) / <alpha-value>)',
        'canvas-dark': 'rgb(var(--canvas-dark) / <alpha-value>)',
        surface:       'rgb(var(--surface) / <alpha-value>)',
        'surface-soft':'rgb(var(--surface-soft) / <alpha-value>)',
        'surface-code':'rgb(var(--surface-code) / <alpha-value>)',

        hairline:       'rgb(var(--hairline) / <alpha-value>)',
        'hairline-soft':'rgb(var(--hairline-soft) / <alpha-value>)',
        'hairline-dark':'rgb(var(--hairline-dark) / <alpha-value>)',

        ink:      'rgb(var(--ink) / <alpha-value>)',
        charcoal: 'rgb(var(--charcoal) / <alpha-value>)',
        slate:    'rgb(var(--slate) / <alpha-value>)',
        steel:    'rgb(var(--steel) / <alpha-value>)',
        stone:    'rgb(var(--stone) / <alpha-value>)',
        muted:    'rgb(var(--muted) / <alpha-value>)',

        primary: {
          DEFAULT: 'rgb(var(--primary) / <alpha-value>)',
          fg:      'rgb(var(--on-primary) / <alpha-value>)',
          pressed: 'rgb(var(--primary-pressed) / <alpha-value>)',
        },

        'brand-green':      'rgb(var(--brand-green) / <alpha-value>)',
        'brand-green-deep': 'rgb(var(--brand-green-deep) / <alpha-value>)',
        'brand-green-soft': 'rgb(var(--brand-green-soft) / <alpha-value>)',
        'brand-tag':        'rgb(var(--brand-tag) / <alpha-value>)',
        'brand-warn':       'rgb(var(--brand-warn) / <alpha-value>)',
        'brand-error':      'rgb(var(--brand-error) / <alpha-value>)',

        'on-dark':       'rgb(var(--on-dark) / <alpha-value>)',
        'on-dark-muted': 'rgb(var(--on-dark-muted) / <alpha-value>)',

        // Semantic
        success: 'rgb(var(--success) / <alpha-value>)',
        warning: 'rgb(var(--warning) / <alpha-value>)',
        danger:  'rgb(var(--danger) / <alpha-value>)',
        info:    'rgb(var(--info) / <alpha-value>)',

        // ---- Legacy aliases (kept so older components still work) ----
        'bg-app':         'rgb(var(--bg-app) / <alpha-value>)',
        'bg-surface':     'rgb(var(--bg-surface) / <alpha-value>)',
        'bg-subtle':      'rgb(var(--bg-subtle) / <alpha-value>)',
        border:           'rgb(var(--border) / <alpha-value>)',
        'border-strong':  'rgb(var(--border-strong) / <alpha-value>)',
        'text-primary':   'rgb(var(--text-primary) / <alpha-value>)',
        'text-secondary': 'rgb(var(--text-secondary) / <alpha-value>)',
        'text-tertiary':  'rgb(var(--text-tertiary) / <alpha-value>)',
        accent: {
          DEFAULT: 'rgb(var(--accent) / <alpha-value>)',
          hover:   'rgb(var(--accent-hover) / <alpha-value>)',
          soft:    'rgb(var(--accent-soft) / <alpha-value>)',
          fg:      'rgb(var(--accent-fg) / <alpha-value>)',
        },
      },
      borderRadius: {
        DEFAULT: '6px',
        xs: '4px',
        sm: '6px',
        md: '8px',
        lg: '12px',
        xl: '16px',
        '2xl': '20px',
        xxl: '24px',
      },
      boxShadow: {
        'soft':    '0 1px 2px rgb(0 0 0 / 0.04)',
        'card':    '0 1px 3px rgb(0 0 0 / 0.05), 0 1px 2px rgb(0 0 0 / 0.04)',
        'overlay': '0 24px 48px -8px rgb(0 0 0 / 0.12), 0 4px 12px -2px rgb(0 0 0 / 0.06)',
      },
      fontSize: {
        // Mintlify-derived scale
        'hero-display':   ['72px',     { lineHeight: '1.05', fontWeight: '600', letterSpacing: '-2px' }],
        'display-lg':     ['56px',     { lineHeight: '1.10', fontWeight: '600', letterSpacing: '-1.5px' }],
        'heading-1':      ['48px',     { lineHeight: '1.10', fontWeight: '600', letterSpacing: '-1px' }],
        'heading-2':      ['36px',     { lineHeight: '1.20', fontWeight: '600', letterSpacing: '-0.5px' }],
        'heading-3':      ['28px',     { lineHeight: '1.25', fontWeight: '600' }],
        'heading-4':      ['22px',     { lineHeight: '1.30', fontWeight: '600' }],
        'heading-5':      ['18px',     { lineHeight: '1.40', fontWeight: '600' }],
        'subtitle':       ['18px',     { lineHeight: '1.50' }],
        'body-md':        ['16px',     { lineHeight: '1.50' }],
        'body-sm':        ['14px',     { lineHeight: '1.50' }],
        'caption':        ['13px',     { lineHeight: '1.40' }],
        'micro':          ['12px',     { lineHeight: '1.40', fontWeight: '500' }],
        'micro-uppercase':['11px',     { lineHeight: '1.40', fontWeight: '600', letterSpacing: '0.5px' }],

        // Backwards-compatible legacy tokens
        'page-title':     ['1.5rem',   { lineHeight: '2rem',   fontWeight: '600' }],
        'section-head':   ['1.125rem', { lineHeight: '1.5rem', fontWeight: '600' }],
        'body':           ['0.875rem', { lineHeight: '1.25rem' }],
        'dense':          ['0.8125rem',{ lineHeight: '1.125rem' }],
      },
      transitionTimingFunction: {
        gentle: 'cubic-bezier(0.4, 0, 0.2, 1)',
      },
      keyframes: {
        'fade-in':        { '0%': { opacity: '0' }, '100%': { opacity: '1' } },
        'scale-in':       { '0%': { opacity: '0', transform: 'scale(0.96)' }, '100%': { opacity: '1', transform: 'scale(1)' } },
        'slide-in-right': { '0%': { transform: 'translateX(100%)' }, '100%': { transform: 'translateX(0)' } },
      },
      animation: {
        'fade-in':        'fade-in 150ms cubic-bezier(0.4, 0, 0.2, 1)',
        'scale-in':       'scale-in 180ms cubic-bezier(0.4, 0, 0.2, 1)',
        'slide-in-right': 'slide-in-right 200ms cubic-bezier(0.4, 0, 0.2, 1)',
      },
    },
  },
  plugins: [],
};

export default config;
