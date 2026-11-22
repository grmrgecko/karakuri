// The app's icon pack is Line Awesome, imported as CSS in app.css: a font plus
// one `.la-name::before { content: "\fxxx" }` rule per icon. That rule set is
// the only list of what the pack holds, so the catalog is read back out of the
// stylesheet rather than hand-maintained beside it — a pack upgrade brings its
// new icons with it and nothing here has to know.
//
// Widgets need this because they render in a shadow root, where the pack's
// class rules do not reach. The glyph itself does: an @font-face declared in
// the document is available to shadow trees, so a widget draws the character
// with the pack's font stack (see ICON_FONT_STACK) and never needs the class.

export interface IconEntry {
  /** Class name as the app writes it, e.g. "la-home". */
  name: string;
  /** The glyph itself, e.g. "". */
  char: string;
}

// ICON_FONT_STACK draws a glyph without knowing which face holds it. Brand
// icons live in a separate font from the rest, and their codepoints do not
// collide, so per-glyph fallback picks the right face on its own. Weight 900
// selects the solid face of the free font; font-synthesis: none keeps the
// browser from faux-bolding the brand face, which has no bold of its own.
export const ICON_FONT_STACK = '"Line Awesome Free", "Line Awesome Brands"';

let cached: IconEntry[] | null = null;

/**
 * lineAwesomeIcons returns every icon the loaded pack defines, sorted by name.
 * The scan runs once: it walks a few thousand CSS rules, and the answer cannot
 * change without a reload.
 */
export function lineAwesomeIcons(): IconEntry[] {
  if (cached) return cached;

  const icons: IconEntry[] = [];
  const seen = new Set<string>();
  for (const sheet of Array.from(document.styleSheets)) {
    let rules: CSSRuleList;
    try {
      rules = sheet.cssRules;
    } catch {
      // A cross-origin stylesheet cannot be read. The pack is bundled with the
      // app, so this is some other sheet and skipping it costs nothing.
      continue;
    }
    for (const rule of Array.from(rules)) {
      const style = rule as CSSStyleRule;
      if (!style.selectorText || !style.style) continue;
      const match = /^\.(la-[a-z0-9-]+)::?before$/i.exec(style.selectorText.trim());
      if (!match) continue;
      const char = unquote(style.style.content);
      if (!char || seen.has(match[1])) continue;
      seen.add(match[1]);
      icons.push({ name: match[1], char });
    }
  }

  icons.sort((a, b) => a.name.localeCompare(b.name));
  cached = icons;
  return icons;
}

// unquote reads the character out of a `content` value, which the CSSOM hands
// back quoted and with the escape already resolved.
function unquote(content: string): string {
  const text = (content || '').trim();
  if (text.length < 2) return '';
  const first = text[0];
  if (first !== '"' && first !== "'") return '';
  return text.slice(1, -1);
}
