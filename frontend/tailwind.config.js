/** @type {import('tailwindcss').Config} */
export default {
  content: ['./src/**/*.{html,js,svelte,ts}'],
  theme: {
    extend: {
      colors: {
        base: 'var(--theme-base)',
        mantle: 'var(--theme-mantle)',
        crust: 'var(--theme-crust)',
        text: 'var(--theme-text)',
        'text-muted': 'var(--theme-text-muted)',
        surface0: 'var(--theme-surface0)',
        surface1: 'var(--theme-surface1)',
        surface2: 'var(--theme-surface2)',
        overlay0: 'var(--theme-overlay0)',
        accent: 'var(--theme-accent)',
        success: 'var(--theme-success)',
        warning: 'var(--theme-warning)',
        error: 'var(--theme-error)',
        info: 'var(--theme-info)',
      },
      fontFamily: {
        sans: ['var(--theme-font-family)', 'Inter', 'sans-serif'],
        mono: ['var(--theme-font-mono)', 'monospace'],
      },
      borderRadius: {
        sm: 'var(--theme-radius-sm)',
        md: 'var(--theme-radius-md)',
        lg: 'var(--theme-radius-lg)',
      },
    },
  },
  plugins: [],
};
