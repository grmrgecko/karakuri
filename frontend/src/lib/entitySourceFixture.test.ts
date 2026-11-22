import { describe, expect, it } from 'vitest';
import { haService, resolveHA } from '../../../plugins/core/widgets/shared/entitySource';
import type { EntitySource } from '../../../plugins/core/widgets/shared/entitySource';
import mapping from '../../../pkg/integrations/homeassistant/testdata/mapping.json';

// The server maps Home Assistant entities for devices in Go; these widgets map
// them for cards. Both read pkg/integrations/homeassistant/testdata/mapping.json,
// so a device and a card row bound to the same entity cannot disagree.
const fixture: any[] = mapping as any[];

// json normalizes a value the way the Go side does: undefined parts drop out.
const json = (v: unknown) => (v === undefined ? undefined : JSON.parse(JSON.stringify(v)));

describe('Home Assistant mapping fixture', () => {
  for (const tc of fixture) {
    it(tc.name, () => {
      const src: EntitySource = { source: 'ha', connectionId: 'c', entityId: tc.entity.entity_id };
      const r = resolveHA(tc.entity, src);
      expect(r.kind).toBe(tc.kind);
      expect(r.readOnly).toBe(tc.readOnly);
      expect(json(r.options)).toEqual(tc.options);
      expect(json(r.value)).toEqual(tc.value);
      for (const w of tc.writes) {
        const call = r.readOnly ? null : haService(src, r, w.attribute, w.value);
        expect(json(call), `${w.attribute} ${JSON.stringify(w.value)}`).toEqual(w.call);
      }
    });
  }
});
