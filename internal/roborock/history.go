package roborock

import (
	"encoding/json"
	"fmt"
	"sort"
)

// RecordIDs accepts both generations of the cleaning-summary response.
func RecordIDs(raw json.RawMessage) ([]int64, error) {
	var summary struct {
		Records []int64 `json:"records"`
	}
	if err := json.Unmarshal(raw, &summary); err != nil {
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil || len(values) < 4 {
			return nil, fmt.Errorf("clean summary has no record list")
		}
		if err := json.Unmarshal(values[3], &summary.Records); err != nil {
			return nil, err
		}
	}
	sort.Slice(summary.Records, func(i, j int) bool { return summary.Records[i] > summary.Records[j] })
	return summary.Records, nil
}

// CleanRecord retains the device fields and adds decoded completion evidence.
type CleanRecord struct {
	ID           int64             `json:"id"`
	RecordKey    string            `json:"record_key,omitempty"`
	Outcome      string            `json:"outcome"`
	FinishReason string            `json:"finish_reason"`
	StartType    string            `json:"start_type"`
	CleanType    string            `json:"clean_type"`
	Error        string            `json:"error"`
	Duration     int64             `json:"duration_seconds"`
	Area         float64           `json:"area_m2"`
	Parts        []json.RawMessage `json:"parts"`
}

func DecodeCleanRecord(id int64, raw json.RawMessage) (CleanRecord, error) {
	r := CleanRecord{ID: id, Outcome: "unknown", FinishReason: "unknown", StartType: "unknown", CleanType: "unknown", Error: "unknown"}
	var parts []json.RawMessage
	if len(raw) > 0 && raw[0] == '{' {
		parts = []json.RawMessage{raw}
	} else if err := json.Unmarshal(raw, &parts); err != nil {
		return r, err
	}
	if len(parts) == 0 {
		return r, fmt.Errorf("empty cleaning record %d", id)
	}
	if parts[0][0] != '{' {
		// Legacy records expose only begin, end, duration, and area.
		if len(parts) == 1 {
			return DecodeCleanRecord(id, parts[0])
		}
		if len(parts) < 4 {
			return r, fmt.Errorf("invalid legacy cleaning record")
		}
		var duration, area int64
		if err := json.Unmarshal(parts[2], &duration); err != nil {
			return r, err
		}
		if err := json.Unmarshal(parts[3], &area); err != nil {
			return r, err
		}
		r.Duration, r.Area, r.Parts = duration, float64(area)/1e6, []json.RawMessage{raw}
		return r, nil
	}
	r.Parts = parts
	for i, part := range parts {
		var record struct {
			Duration int64 `json:"duration"`
			Area     int64 `json:"area"`
			Complete *int  `json:"complete"`
			Finish   *int  `json:"finish_reason"`
			Start    *int  `json:"start_type"`
			Clean    *int  `json:"clean_type"`
			Error    *int  `json:"error"`
		}
		if err := json.Unmarshal(part, &record); err != nil {
			return r, err
		}
		r.Duration += record.Duration
		r.Area += float64(record.Area) / 1e6
		if i == 0 {
			r.StartType = codeName(reference.StartTypes, record.Start)
		}
		if i != len(parts)-1 {
			continue
		}
		r.FinishReason = codeName(reference.FinishReasons, record.Finish)
		r.CleanType = codeName(reference.CleanTypes, record.Clean)
		if record.Complete != nil {
			if *record.Complete == 1 {
				r.Outcome = "completed"
			} else {
				r.Outcome = "incomplete"
			}
		} else if r.FinishReason == "finished_cleaning" {
			r.Outcome = "completed"
		}
		if r.FinishReason == "manual_interrupt" {
			r.Outcome = "interrupted"
		}
		if record.Error != nil {
			r.Error = ErrorName(*record.Error)
		}
	}
	return r, nil
}

func codeName(values map[string]string, code *int) string {
	if code == nil {
		return "unknown"
	}
	if name, ok := values[fmt.Sprint(*code)]; ok {
		return name
	}
	return fmt.Sprintf("unknown (%d)", *code)
}
