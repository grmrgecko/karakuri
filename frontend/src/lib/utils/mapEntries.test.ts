import { describe, expect, it } from 'vitest';
import { mapObject, mapRows, type MapEntry } from './mapEntries';

// typeKey replays the map control typing a key into one row a character at a
// time: each keystroke edits the rows drawn from the stored value and the
// draft, and stores the object written from the edited rows.
function typeKey(stored: unknown, row: number, key: string) {
  let draft: MapEntry[] | undefined;
  let rows = mapRows(stored, draft);
  for (let n = 1; n <= key.length; n++) {
    draft = rows.map(([k, v], j) => (j === row ? [key.slice(0, n), v] : [k, v]));
    stored = mapObject(draft);
    rows = mapRows(stored, draft);
  }
  return { stored, rows };
}

// The map control drew its rows from the stored object on every keystroke.
// A key typed through another row's key merged the two rows, and an
// integer-like key moved to the front of the object, so the rows swapped
// under the focused input and the next keystroke renamed the other entry.
describe('map control rows', () => {
  it('keeps both rows while a new key passes through an existing one', () => {
    // Typing "10" passes through "1", the key of the existing row.
    const { stored, rows } = typeKey({ '1': 'On', '': '' }, 1, '10');
    expect(rows).toEqual([
      ['1', 'On'],
      ['10', ''],
    ]);
    expect(stored).toEqual({ '1': 'On', '10': '' });
  });

  it('keeps row order when a key is integer-like', () => {
    const { stored, rows } = typeKey({ heat: 'Heating', '': '' }, 1, '0x');
    expect(rows).toEqual([
      ['heat', 'Heating'],
      ['0x', ''],
    ]);
    expect(stored).toEqual({ heat: 'Heating', '0x': '' });
  });
});
