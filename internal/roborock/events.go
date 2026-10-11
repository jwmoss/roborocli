package roborock

import (
	"encoding/json"
	"sync/atomic"
	"time"
)

type Event struct {
	Time    time.Time                  `json:"time"`
	Kind    string                     `json:"kind"`
	Data    map[string]json.RawMessage `json:"data,omitempty"`
	Dropped uint64                     `json:"dropped,omitempty"`
	Error   string                     `json:"error,omitempty"`
}

type eventStream struct {
	events  chan Event
	dropped atomic.Uint64
}

func (s *eventStream) emit(event Event) {
	if s.events == nil {
		return
	}
	event.Time = time.Now().UTC()
	event.Dropped = s.dropped.Swap(0)
	select {
	case s.events <- event:
	default:
		s.dropped.Add(event.Dropped + 1)
	}
}

func (s *eventStream) push(points map[string]json.RawMessage, family string) {
	data := map[string]json.RawMessage{}
	for code, value := range points {
		if family == "v1" && code == "102" {
			continue
		}
		name := reference.Datapoints[family][code]
		if name == "" {
			name = code
		}
		data[name] = value
	}
	if len(data) > 0 {
		s.emit(Event{Kind: "push", Data: data})
	}
}

func (c *LocalClient) Events() <-chan Event { return c.stream.events }
func (m *MQTTClient) Events() <-chan Event  { return m.stream.events }
