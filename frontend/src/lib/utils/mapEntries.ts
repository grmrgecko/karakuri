// MapEntry is one key and value row of the map control.
export type MapEntry = [string, string];

// mapObject writes the map control's rows as the stored object, or undefined
// when there are none. The first row with a key wins, so a row whose key
// passes through an existing one while it is typed does not overwrite that
// entry's value.
export function mapObject(entries: MapEntry[]): Record<string, string> | undefined {
  const obj: Record<string, string> = {};
  for (const [k, v] of entries) {
    if (!Object.hasOwn(obj, k)) obj[k] = v;
  }
  return entries.length > 0 ? obj : undefined;
}

// mapRows returns the rows the map control draws for a stored value. The
// draft is the control's own rows as last edited; it is kept while it still
// writes the stored object, because an object cannot hold two rows with the
// same key and puts integer-like keys first, which would merge or reorder rows
// under a key being typed. A stored value changed from elsewhere replaces it.
export function mapRows(stored: unknown, draft: MapEntry[] | undefined): MapEntry[] {
  const rows: MapEntry[] =
    stored && typeof stored === 'object' && !Array.isArray(stored)
      ? Object.entries(stored as Record<string, unknown>).map(([k, val]) => [k, String(val ?? '')])
      : [];
  if (draft && sameMap(mapObject(draft), rows)) return draft;
  return rows;
}

function sameMap(obj: Record<string, string> | undefined, rows: MapEntry[]): boolean {
  const keys = Object.keys(obj ?? {});
  return keys.length === rows.length && rows.every(([k, v]) => obj !== undefined && Object.hasOwn(obj, k) && obj[k] === v);
}
