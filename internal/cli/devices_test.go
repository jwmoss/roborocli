package cli

import (
	"encoding/json"
	"testing"
)

func rowMap(rows [][]string) map[string]string {
	out := map[string]string{}
	for _, row := range rows {
		out[row[0]] = row[1]
	}
	return out
}

func TestCleanSummaryRowsReadsBothKeyForms(t *testing.T) {
	for name, raw := range map[string]string{
		"short": `{"clean_time":103057,"clean_area":1394212500,"clean_count":20,"records":[1791641765]}`,
		"total": `{"total_clean_time":103057,"total_clean_area":1394212500,"total_clean_count":20}`,
	} {
		var summary map[string]any
		if err := json.Unmarshal([]byte(raw), &summary); err != nil {
			t.Fatal(err)
		}
		rows := rowMap(cleanSummaryRows(summary))
		if rows["total_clean_time"] != "1d 4h 37m" {
			t.Errorf("%s: total_clean_time = %q", name, rows["total_clean_time"])
		}
		if rows["total_clean_area"] != "1394.2 m2" {
			t.Errorf("%s: total_clean_area = %q", name, rows["total_clean_area"])
		}
		if rows["total_clean_count"] != "20" {
			t.Errorf("%s: total_clean_count = %q", name, rows["total_clean_count"])
		}
	}
}

func TestConsumableRowsAcceptsArrayForm(t *testing.T) {
	rows, err := consumableRows(json.RawMessage(`[{"main_brush_work_time":7200,"dust_collection_work_times":16}]`))
	if err != nil {
		t.Fatalf("consumableRows: %v", err)
	}
	got := rowMap(rows)
	if got["main_brush_work_time"] != "2h" {
		t.Errorf("main_brush_work_time = %q", got["main_brush_work_time"])
	}
	if got["dust_collection_work_times"] != "16" {
		t.Errorf("dust_collection_work_times = %q", got["dust_collection_work_times"])
	}
}

func TestFormatArea(t *testing.T) {
	if got := formatArea(108517500); got != "108.5 m2" {
		t.Fatalf("formatArea = %q", got)
	}
}

func TestParseRoomMappingForms(t *testing.T) {
	for name, raw := range map[string]string{
		"tuples":  `[[1,"22132102",13],[5,"22132094",14]]`,
		"objects": `[{"id":1,"name":"Dining room"},{"id":5,"name":"Kitchen"}]`,
		"map":     `{"1":"Dining room","5":"Kitchen"}`,
	} {
		rooms, err := parseRoomMapping(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(rooms) != 2 || rooms[0].ID != 1 || rooms[1].ID != 5 {
			t.Fatalf("%s: rooms = %+v", name, rooms)
		}
	}
}

func TestNameRooms(t *testing.T) {
	rooms := []room{{ID: 1, NameID: "22132102"}, {ID: 5, NameID: "22132094"}, {ID: 6, NameID: "-1"}}
	names := map[int64]string{22132102: "Dining room"}
	if nameRooms(rooms, names) {
		t.Fatal("expected a missing name for room 5")
	}
	if rooms[0].Name != "Dining room" {
		t.Fatalf("rooms[0].Name = %q", rooms[0].Name)
	}
	names[22132094] = "Kitchen"
	if !nameRooms(rooms, names) || rooms[1].Name != "Kitchen" {
		t.Fatalf("rooms = %+v", rooms)
	}
}

func TestResolveRooms(t *testing.T) {
	rooms := []room{{ID: 1, Name: "Dining room"}, {ID: 5, Name: "Kitchen"}}
	ids, err := resolveRooms([]string{"kitchen", "Dining Room", "5"}, rooms)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != 5 || ids[1] != 1 || ids[2] != 5 {
		t.Fatalf("ids = %v", ids)
	}
	if _, err := resolveRooms([]string{"garage"}, rooms); err == nil {
		t.Fatal("expected an error for an unknown room")
	}
}

func TestSegmentCleanParams(t *testing.T) {
	got, _ := json.Marshal(segmentCleanParams([]int64{5, 1}, 2))
	if string(got) != `[{"repeat":2,"segments":[5,1]}]` {
		t.Fatalf("params = %s", got)
	}
}
