import { describe, expect, it } from 'vitest';
import { drawsLocation, resolveHA } from '../../../plugins/core/widgets/shared/entitySource';
import type { EntitySource } from '../../../plugins/core/widgets/shared/entitySource';

// Home Assistant persons and trackers are drawn as read-only locations. The
// value comes from the attributes both domains report, and since from the
// entity's last_changed, so a row says how long someone has been where they are.
describe('resolveHA for locations', () => {
  const person = {
    entity_id: 'person.james',
    state: 'home',
    last_changed: '2026-10-31T14:02:11Z',
    attributes: { friendly_name: 'James', latitude: 41.88, longitude: -87.63, gps_accuracy: 25, source: 'device_tracker.phone' },
  };
  const tracker = {
    entity_id: 'device_tracker.phone',
    state: 'Work',
    last_changed: '2026-10-31T09:00:00Z',
    attributes: { source_type: 'gps' },
  };
  const src = (entityId: string): EntitySource => ({ source: 'ha', connectionId: 'c', entityId });

  it('draws a person as a read-only location', () => {
    const r = resolveHA(person, src(person.entity_id));
    expect(r.kind).toBe('location');
    expect(r.readOnly).toBe(true);
    expect(r.value).toEqual({
      state: 'home',
      latitude: 41.88,
      longitude: -87.63,
      accuracy: 25,
      source: 'device_tracker.phone',
      since: '2026-10-31T14:02:11Z',
    });
    expect(r.options.zones).toEqual([
      { value: 'home', label: 'Home' },
      { value: 'not_home', label: 'Away' },
    ]);
  });

  it('takes a tracker source from source_type and leaves missing coordinates out', () => {
    const r = resolveHA(tracker, src(tracker.entity_id));
    expect(r.readOnly).toBe(true);
    expect(r.value).toMatchObject({ state: 'Work', source: 'gps', since: '2026-10-31T09:00:00Z' });
    expect((r.value as Record<string, unknown>).latitude).toBeUndefined();
  });

  it('marks unavailable and unknown states', () => {
    expect(resolveHA({ ...person, state: 'unavailable' }, src(person.entity_id)).unavailable).toBe(true);
    expect(resolveHA({ ...person, state: 'unknown' }, src(person.entity_id)).unavailable).toBe(true);
  });

  it('turns on coordinates from the row', () => {
    const r = resolveHA(person, { ...src(person.entity_id), showCoordinates: true });
    expect(r.options.show_coordinates).toBe(true);
  });
});

describe('drawsLocation', () => {
  it('recognises each source that resolves to a location', () => {
    const controls = [{ id: 'c1', kind: 'location' }, { id: 'c2', kind: 'select' }];
    const devices = [{ id: 'd1', control: 'location' }];
    expect(drawsLocation({ source: 'ha', entityId: 'person.james' }, controls, devices)).toBe(true);
    expect(drawsLocation({ source: 'ha', entityId: 'light.desk' }, controls, devices)).toBe(false);
    expect(drawsLocation({ source: 'control', controlId: 'c1' }, controls, devices)).toBe(true);
    expect(drawsLocation({ source: 'control', controlId: 'c2' }, controls, devices)).toBe(false);
    expect(drawsLocation({ source: 'mqtt', deviceId: 'd1' }, controls, devices)).toBe(true);
    expect(drawsLocation({ source: 'mqtt', deviceId: 'd1', deviceField: 'battery' }, controls, devices)).toBe(false);
  });
});
