// Icons come from the host's own pack (Line Awesome), not from a widget's own
// set: a dashboard reads as one UI, and the pack is already loaded for the app
// around it. The host hands over the catalog and the font stack through
// `window.__karakuri`; the class rules that normally draw an icon do not cross
// into a shadow root, but the pack's @font-face does, so a widget renders the
// glyph character itself with that stack.
//
// A stored value is the class name, e.g. "la-home". Anything else is drawn as
// the text it is, which is what keeps the emoji in placements written before
// the picker existed working.

export interface IconEntry {
  name: string;
  char: string;
}

/** ICON_FONT_STACK is the fallback used when the host is too old to name one. */
export const ICON_FONT_STACK = '"Line Awesome Free", "Line Awesome Brands"';

const bridge = () => (window as any).__karakuri;

/** loadIcons returns the host's icon catalog, or nothing if it offers none. */
export function loadIcons(): IconEntry[] {
  try {
    const list = bridge()?.icons?.();
    return Array.isArray(list) ? list : [];
  } catch {
    return [];
  }
}

/** iconFontStack is the font stack the host draws its own icons with. */
export function iconFontStack(): string {
  return bridge()?.iconFontStack || ICON_FONT_STACK;
}

// Lookups are keyed by name once per catalog: a card of rows resolves its
// icons on every render, and the pack is well over a thousand entries.
const indexes = new WeakMap<IconEntry[], Map<string, IconEntry>>();

function index(icons: IconEntry[]): Map<string, IconEntry> {
  let map = indexes.get(icons);
  if (!map) {
    map = new Map(icons.map((i) => [i.name.toLowerCase(), i]));
    indexes.set(icons, map);
  }
  return map;
}

/** isIconName reports whether a value names a pack icon rather than plain text. */
export function isIconName(value: string): boolean {
  return /^la-[a-z0-9-]+$/i.test((value || '').trim());
}

/**
 * iconGlyph resolves what to draw for a stored value: the pack's character
 * when it names an icon, and otherwise the value itself, so an emoji or a
 * letter still shows. `glyph` says which, because only a glyph wants the
 * pack's font.
 */
export function iconGlyph(value: string, icons: IconEntry[]): { text: string; glyph: boolean } {
  const name = (value || '').trim();
  if (!name) return { text: '', glyph: false };
  if (!isIconName(name)) return { text: name, glyph: false };
  const entry = index(icons).get(name.toLowerCase());
  // A name the pack does not hold draws nothing rather than a tofu box: the
  // pack was upgraded or the value was typed by hand, and an empty slot reads
  // better on a wall than a missing-glyph rectangle.
  return entry ? { text: entry.char, glyph: true } : { text: '', glyph: false };
}

/**
 * filterIcons narrows the catalog to what has been typed. The `la-` prefix is
 * ignored while matching, so typing "home" finds it without knowing the pack's
 * naming, and an exact match offers the whole list back rather than the one
 * entry the author is trying to replace.
 */
export function filterIcons(icons: IconEntry[], term: string, limit = 60): IconEntry[] {
  const text = (term || '').trim().toLowerCase().replace(/^la-/, '');
  if (!text) return icons.slice(0, limit);
  const exact = icons.some((i) => i.name.toLowerCase() === `la-${text}`);
  if (exact) return icons.slice(0, limit);
  const matches = icons.filter((i) => i.name.toLowerCase().includes(text));
  // A name that starts with what was typed is the likelier intent, so it sorts
  // ahead of one that merely contains it.
  matches.sort((a, b) => {
    const aStarts = a.name.toLowerCase().startsWith(`la-${text}`) ? 0 : 1;
    const bStarts = b.name.toLowerCase().startsWith(`la-${text}`) ? 0 : 1;
    return aStarts - bStarts || a.name.length - b.name.length;
  });
  return matches.slice(0, limit);
}
