import { describe, expect, it } from 'vitest';
import { haService, mediaOptions, resolveHA } from '../../../plugins/core/widgets/shared/entitySource';
import type { EntitySource } from '../../../plugins/core/widgets/shared/entitySource';

// Home Assistant media players are drawn as the media kind: the value from the
// state and attributes, the controls from supported_features, and each press
// as the matching media_player service. The lab Home Assistant is read only,
// so these calls are the check that a press would reach the right service.
describe('resolveHA for media players', () => {
  const src: EntitySource = { source: 'ha', connectionId: 'c', entityId: 'media_player.kitchen' };
  const speaker = {
    entity_id: 'media_player.kitchen',
    state: 'playing',
    attributes: {
      friendly_name: 'Kitchen',
      // pause, seek, volume set, mute, previous, next, select source, stop, play
      supported_features: 1 | 2 | 4 | 8 | 16 | 32 | 2048 | 4096 | 16384,
      volume_level: 0.34,
      is_volume_muted: false,
      media_title: 'Song',
      media_artist: 'Band',
      media_album_name: 'Album',
      entity_picture: '/api/media_player_proxy/media_player.kitchen?token=x',
      media_position: 61,
      media_duration: 200,
      media_position_updated_at: '2026-11-06T08:00:00+00:00',
      source: 'Radio',
      source_list: ['Radio', 'TV'],
    },
  };

  it('maps state and attributes into the kind', () => {
    const r = resolveHA(speaker, src);
    expect(r.kind).toBe('media');
    expect(r.readOnly).toBe(false);
    expect(r.value).toEqual({
      state: 'playing',
      volume: 34,
      muted: false,
      title: 'Song',
      artist: 'Band',
      album: 'Album',
      art: '/api/media_player_proxy/media_player.kitchen?token=x',
      position: 61,
      duration: 200,
      position_at: '2026-11-06T08:00:00+00:00',
      source: 'Radio',
      sources: ['Radio', 'TV'],
    });
    expect(r.options).toEqual({
      volume: true, mute: true, skip: true, seek: true, source: true, power: false, stop: true,
    });
  });

  it('reads standby as off and leaves missing attributes out', () => {
    const r = resolveHA({ entity_id: 'media_player.tv', state: 'standby', attributes: {} }, src);
    const value = r.value as Record<string, unknown>;
    expect(value.state).toBe('off');
    expect(value.volume).toBeUndefined();
    expect(value.sources).toBeUndefined();
  });

  it('offers only the features a player reports', () => {
    expect(mediaOptions(0)).toEqual({
      volume: false, mute: false, skip: false, seek: false, source: false, power: false, stop: false,
    });
    // A Chromecast-style player: pause, volume, mute, on, off, play.
    expect(mediaOptions(1 | 4 | 8 | 128 | 256 | 16384)).toEqual({
      volume: true, mute: true, skip: false, seek: false, source: false, power: true, stop: false,
    });
    expect(mediaOptions(16).skip).toBe(true);
  });

  it('turns each write into its media_player service', () => {
    const r = resolveHA(speaker, src);
    const cases: [string, unknown, string, Record<string, unknown> | undefined][] = [
      ['state', 'playing', 'media_play', undefined],
      ['state', 'paused', 'media_pause', undefined],
      ['state', 'idle', 'media_stop', undefined],
      ['state', 'on', 'turn_on', undefined],
      ['state', 'off', 'turn_off', undefined],
      ['volume', 30, 'volume_set', { volume_level: 0.3 }],
      ['muted', true, 'volume_mute', { is_volume_muted: true }],
      ['source', 'TV', 'select_source', { source: 'TV' }],
      ['position', 90, 'media_seek', { seek_position: 90 }],
      ['command', 'next', 'media_next_track', undefined],
      ['command', 'previous', 'media_previous_track', undefined],
    ];
    for (const [attribute, value, service, data] of cases) {
      expect(haService(src, r, attribute, value)).toEqual({ domain: 'media_player', service, data });
    }
    expect(haService(src, r, 'state', 'buffering')).toBeNull();
    expect(haService(src, r, 'command', 'shuffle')).toBeNull();
  });
});
