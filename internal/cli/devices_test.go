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
