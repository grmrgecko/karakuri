import { describe, expect, it } from 'vitest';
import { applyDeviceMessage, resolveDevice } from '../../../plugins/core/widgets/shared/entitySource';

// A device that is offline, unknown, or gone from Zigbee2MQTT draws as
// unavailable, values and field rows alike, and an availability message
// updates that without blanking the values it does not carry.
describe('MQTT device availability', () => {
  const src = { source: 'mqtt' as const, deviceId: 'd1' };

  it('draws offline, unknown, and missing devices as unavailable', () => {
    const base = { id: 'd1', control: 'boolean', state: { on: true } };
    expect(resolveDevice({ ...base, availability: 'online' }, base.state, src).unavailable).toBe(false);
    expect(resolveDevice({ ...base }, base.state, src).unavailable).toBe(false);
    expect(resolveDevice({ ...base, availability: 'offline' }, base.state, src).unavailable).toBe(true);
    expect(resolveDevice({ ...base, availability: 'unknown' }, base.state, src).unavailable).toBe(true);
    expect(resolveDevice({ ...base, availability: 'online', missing: true }, base.state, src).unavailable).toBe(true);
    expect(resolveDevice({ ...base, availability: 'offline' }, base.state, { ...src, deviceField: 'battery' }).unavailable).toBe(true);
  });

  it('keeps values when a message carries availability alone', () => {
    const device: any = { id: 'd1', control: 'boolean', state: { on: true }, availability: 'online', stale: true };
    expect(applyDeviceMessage(device, { device_id: 'd1', availability: 'offline', previous: 'online' })).toBe(false);
    expect(device.state).toEqual({ on: true });
    expect(device.availability).toBe('offline');
    expect(device.stale).toBe(true);

    expect(applyDeviceMessage(device, { state: { on: false }, availability: 'online' })).toBe(true);
    expect(device.state).toEqual({ on: false });
    expect(device.availability).toBe('online');
    expect(device.stale).toBe(false);
  });
});

describe('Zigbee2MQTT missing devices', () => {
  it('follows a missing message', () => {
    const device: any = { id: 'd1', control: 'boolean', state: { on: true }, availability: 'online' };
    applyDeviceMessage(device, { device_id: 'd1', missing: true });
    expect(resolveDevice(device, device.state, { source: 'mqtt', deviceId: 'd1' }).unavailable).toBe(true);
    applyDeviceMessage(device, { device_id: 'd1', missing: false });
    expect(device.missing).toBe(false);
  });
});
