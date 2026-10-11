package roborock

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// DecodeFamilyHistory uses the start timestamp as the CLI id for both B01 families.
// Q10's separate map-record key remains available as record_key.
func DecodeFamilyHistory(family string, raw json.RawMessage) ([]CleanRecord, error) {
	var records []CleanRecord
	switch family {
	case "q7":
		var response struct {
			Records []struct {
				Detail string `json:"detail"`
			} `json:"record_list"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return nil, err
		}
		for _, item := range response.Records {
			record, err := decodeQ7Record(json.RawMessage(item.Detail))
			if err != nil {
				return nil, err
			}
			records = append(records, record)
		}
	case "q10":
		var response map[string]struct {
			Result int      `json:"result"`
			Data   []string `json:"data"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return nil, err
		}
		if response["52"].Result != 1 {
			return nil, fmt.Errorf("Q10 history request failed")
		}
		for _, item := range response["52"].Data {
			record, err := decodeQ10Record(item)
			if err != nil {
				return nil, err
			}
			records = append(records, record)
		}
	default:
		return nil, fmt.Errorf("%s does not expose vacuum cleaning history", family)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID > records[j].ID })
	return records, nil
}

func decodeQ7Record(raw json.RawMessage) (CleanRecord, error) {
	var detail struct {
		Start    int64 `json:"record_start_time"`
		Duration int64 `json:"record_use_time"`
		Area     int64 `json:"record_clean_area"`
		Fault    *int  `json:"record_faultcode"`
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		return CleanRecord{}, err
	}
	if detail.Start <= 0 || detail.Duration < 0 || detail.Area < 0 {
		return CleanRecord{}, fmt.Errorf("invalid Q7 cleaning record")
	}
	// Q7 task/source/fault codes do not share V1 meanings. Keep them in parts.
	r := CleanRecord{ID: detail.Start, Duration: detail.Duration, Area: float64(detail.Area) / 100,
		Outcome: "unknown", FinishReason: "unknown", StartType: "unknown", CleanType: "unknown",
		Error: codeName(nil, detail.Fault), Parts: []json.RawMessage{raw}}
	if detail.Fault != nil && *detail.Fault == 0 {
		r.Error = "none"
	}
	return r, nil
}

func decodeQ10Record(raw string) (CleanRecord, error) {
	fields := strings.Split(raw, "_")
	if len(fields) != 12 || fields[0] == "" {
		return CleanRecord{}, fmt.Errorf("invalid Q10 cleaning record")
	}
	values := make([]int, len(fields))
	for i := 1; i < len(fields); i++ {
		value, err := strconv.Atoi(fields[i])
		if err != nil || value < 0 {
			return CleanRecord{}, fmt.Errorf("invalid Q10 record field %d", i)
		}
		values[i] = value
	}
	part, _ := json.Marshal(raw)
	outcome := codeName(map[string]string{"0": "interrupted", "1": "completed", "2": "stopped"}, &values[9])
	return CleanRecord{ID: int64(values[1]), RecordKey: fields[0], Duration: int64(values[2]) * 60, Area: float64(values[3]),
		Outcome: outcome, FinishReason: outcome, Error: "unknown",
		StartType: codeName(map[string]string{"0": "remote", "1": "app", "2": "timer", "3": "button"}, &values[10]),
		CleanType: codeName(map[string]string{"0": "full", "1": "selective_room", "3": "zone", "4": "spot"}, &values[7]),
		Parts:     []json.RawMessage{part}}, nil
}
