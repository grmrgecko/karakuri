// Theme colours read from the dashboard's --theme-* custom properties, which
// reach the widget's shadow root by inheritance.

import type { Palette } from "./data/types";
import { FALLBACK_PALETTE } from "./data/colors";

// SERIES is the palette-classic order of theme colour names.
const SERIES = ["blue", "green", "peach", "mauve", "red", "yellow", "teal", "sky", "lavender", "pink"];

// NAMED maps chart colour names to theme colour names.
const NAMED: Record<string, string> = {
  blue: "blue",
  green: "green",
  orange: "peach",
  purple: "mauve",
  red: "red",
  yellow: "yellow",
  teal: "teal",
  sky: "sky",
  lavender: "lavender",
  pink: "pink",
  text: "text",
};

/** readPalette reads the theme's colours from an element, falling back to Catppuccin Mocha. */
export function readPalette(el: Element): Palette {
  const css = getComputedStyle(el);
  const v = (name: string, fallback: string) => css.getPropertyValue(`--theme-${name}`).trim() || fallback;
  const fb = FALLBACK_PALETTE;
  const named: Record<string, string> = {};
  for (const [colour, theme] of Object.entries(NAMED)) named[colour] = v(theme, fb.named[colour]);
  return {
    series: SERIES.map((name, i) => v(name, fb.series[i])),
    named,
    text: v("text", fb.text),
    textMuted: v("text-muted", v("subtext0", fb.textMuted)),
    border: v("border", v("surface1", fb.border)),
    background: v("widget-bg", v("base", fb.background)),
    surface: v("surface0", fb.surface),
    font: v("font-family", fb.font),
  };
}

/** watchTheme calls back when the page's theme may have changed. */
export function watchTheme(onChange: () => void): () => void {
  const observer = new MutationObserver(() => onChange());
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ["style", "class", "data-theme"] });
  observer.observe(document.head, { childList: true });
  return () => observer.disconnect();
}
