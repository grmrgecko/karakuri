package hatest

// SetHouse fills the registries with a small house the registry tests share:
// a ground and an upper floor, a kitchen on the ground floor and a bedroom
// upstairs, a hallway with no floor, a kitchen speaker device, a motion
// sensor device labelled night, and entities that place themselves, inherit
// their device's area, sit nowhere, or are hidden or diagnostic.
func (s *Server) SetHouse() {
	s.SetRegistry("floor", []map[string]any{
		{"floor_id": "upstairs", "name": "Upstairs", "level": 1},
		{"floor_id": "ground", "name": "Ground floor", "level": 0},
	})
	s.SetRegistry("area", []map[string]any{
		{"area_id": "kitchen", "name": "Kitchen", "floor_id": "ground", "aliases": []string{}, "labels": []string{"away"}},
		{"area_id": "bedroom", "name": "Bedroom", "floor_id": "upstairs", "aliases": []string{}},
		{"area_id": "hallway", "name": "Hallway", "floor_id": nil, "aliases": []string{}},
	})
	s.SetRegistry("device", []map[string]any{
		{"id": "dev-speaker", "name": "Speaker", "name_by_user": "Kitchen speaker", "area_id": "kitchen",
			"manufacturer": "Acme", "model": "S1", "labels": []string{}, "disabled_by": nil},
		{"id": "dev-motion", "name": "Motion sensor", "name_by_user": nil, "area_id": "hallway",
			"manufacturer": "Acme", "model": "M1", "labels": []string{"night"}, "disabled_by": nil},
		{"id": "dev-old", "name": "Retired", "area_id": "kitchen", "labels": []string{}, "disabled_by": "user"},
	})
	s.SetRegistry("entity", []map[string]any{
		{"ei": "light.kitchen_ceiling", "ai": "kitchen", "en": "Ceiling"},
		{"ei": "light.bedroom_lamp", "ai": "bedroom", "lb": []string{"night"}},
		{"ei": "media_player.kitchen_speaker", "di": "dev-speaker"},
		{"ei": "sensor.kitchen_speaker_signal", "di": "dev-speaker", "ec": 1},
		{"ei": "binary_sensor.hallway_motion", "di": "dev-motion"},
		{"ei": "light.hidden_strip", "ai": "kitchen", "hb": true},
		{"ei": "sun.sun"},
	})
	s.SetRegistry("label", []map[string]any{
		{"label_id": "night", "name": "Night", "color": "indigo"},
	})
}
