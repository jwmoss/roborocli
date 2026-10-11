package roborock

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

func (m *MQTTClient) familyRooms(ctx context.Context, duid string) (json.RawMessage, error) {
	raw, err := m.familyMap(ctx, duid)
	if err != nil {
		return nil, err
	}
	var floor *MapData
	if m.family == "q7" {
		floor, err = ParseQ7Map(raw)
	} else {
		floor, err = ParseQ10Map(raw)
	}
	if err != nil {
		return nil, err
	}
	type room struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	rooms := make([]room, 0, len(floor.Rooms))
	for _, r := range floor.Rooms {
		name := r.Name
		if name == "" {
			name = fmt.Sprintf("Room %d", r.ID)
		}
		rooms = append(rooms, room{r.ID, name})
	}
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].ID < rooms[j].ID })
	return json.Marshal(rooms)
}

func (m *MQTTClient) cleanFamilyRooms(ctx context.Context, duid string, params any) (json.RawMessage, error) {
	var requests []struct {
		Segments []int `json:"segments"`
		Repeat   int   `json:"repeat"`
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(raw, &requests) != nil || len(requests) != 1 || len(requests[0].Segments) == 0 {
		return nil, fmt.Errorf("invalid room-clean request")
	}
	r := requests[0]
	if r.Repeat < 1 || r.Repeat > 3 {
		return nil, fmt.Errorf("repeat must be 1-3")
	}
	if m.family == "q7" {
		if r.Repeat > 2 {
			return nil, fmt.Errorf("Q7 supports at most two passes")
		}
		if _, err := m.callQ7(ctx, duid, "prop.set", map[string]int{"repeat_state": r.Repeat - 1}); err != nil {
			return nil, err
		}
		return m.callQ7(ctx, duid, "service.set_room_clean", map[string]any{"clean_type": 1, "ctrl_value": 1, "room_ids": r.Segments})
	}
	if m.family == "q10" {
		if err := m.publishFamily(ctx, duid, map[string]any{"136": r.Repeat}); err != nil {
			return nil, err
		}
		if err := m.publishFamily(ctx, duid, map[string]any{"201": map[string]any{"cmd": 2, "clean_paramters": r.Segments}}); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"published":true,"verified":false}`), nil
	}
	return nil, fmt.Errorf("%s has no room-clean command", m.family)
}
