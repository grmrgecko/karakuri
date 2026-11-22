import type { TileStyle } from './stores/dashboards';

// A placement's style is applied by the host to the cell that holds the
// widget, so it reaches every package without the package knowing. Colors
// are set both as the cell's own properties and as the theme's custom
// properties, which cross a widget's shadow root: a widget that reads the
// theme's text or accent color follows the placement's.

/** safeColor drops a value that could end the declaration it is written into. */
export function safeColor(value: string | undefined): string {
  const v = (value ?? '').trim();
  if (!v || v.length > 64 || /[;{}<>\\"']/.test(v) || /url\(/i.test(v)) return '';
  return v;
}

/** isTransparent reports whether a style drops the tile face. */
export function isTransparent(style: TileStyle | null | undefined): boolean {
  return safeColor(style?.background).toLowerCase() === 'transparent';
}

/**
 * tileStyleCSS returns the declarations a placement's style adds to its cell,
 * or an empty string when it has none.
 */
export function tileStyleCSS(style: TileStyle | null | undefined): string {
  if (!style) return '';
  const out: string[] = [];
  const background = safeColor(style.background);
  if (background.toLowerCase() === 'transparent') {
    out.push(
      'background:transparent',
      'border-color:transparent',
      'box-shadow:none',
      '--theme-widget-bg:transparent',
      '--tile-border:transparent'
    );
  } else if (background) {
    out.push(`background:${background}`, `--theme-widget-bg:${background}`);
  }
  const borderColor = safeColor(style.border_color);
  const width = Math.max(0, Math.min(16, Math.round(Number(style.border_width) || 0)));
  if (width > 0) {
    out.push(`border:${width}px solid ${borderColor || 'var(--theme-border, #45475a)'}`);
  } else if (borderColor) {
    out.push(`border:1px solid ${borderColor}`);
  }
  if (borderColor) out.push(`--tile-border:${borderColor}`);
  if (typeof style.radius === 'number' && Number.isFinite(style.radius)) {
    const r = Math.max(0, Math.min(64, Math.round(style.radius)));
    out.push(`border-radius:${r}px`, `--theme-radius-md:${r}px`);
  }
  const text = safeColor(style.text_color);
  if (text) out.push(`color:${text}`, `--theme-text:${text}`);
  const accent = safeColor(style.accent_color);
  if (accent) out.push(`--theme-accent:${accent}`);
  return out.length ? out.join(';') + ';' : '';
}
