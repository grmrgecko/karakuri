import { describe, expect, it } from 'vitest';
import { classifyPorts } from './payloadPorts';

// The classifier routes to flow.ClassifyPortID(category), which lowercases
// with Go's strings.ToLower: one rune at a time, by the simple case mapping.
// String.prototype.toLowerCase applies the full mapping, which writes a
// capital sigma that ends a word as the final form and a dotted capital I as
// i plus a combining dot. The editor drew those categories on a port the
// server does not have, so the edge from it was refused on save.
describe('classifyPorts', () => {
  it.each([
    ['a word ending in capital sigma', 'ΤΕΛΟΣ', 'cat:τελοσ'],
    ['a dotted capital I', 'İptal', 'cat:iptal'],
    ['plain ASCII', 'Urgent', 'cat:urgent'],
  ])('names the port the server routes to for %s', (_name, category, want) => {
    expect(classifyPorts({ categories: category }).map((p) => p.id)).toEqual([want]);
  });

  it('drops a repeat the server drops', () => {
    const ports = classifyPorts({ categories: 'ΤΕΛΟΣ, τελοσ, Other' });
    expect(ports.map((p) => p.label), 'both spellings lowercase to τελοσ on the server').toEqual(['ΤΕΛΟΣ', 'Other']);
  });
});
