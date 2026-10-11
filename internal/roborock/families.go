package roborock

import (
	"context"
	"crypto/aes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func Family(device Device, product Product) (string, error) {
	switch device.PV {
	case "", "1.0", "L01":
		return "v1", nil
	case "A01":
		if strings.HasPrefix(product.Model, "roborock.wm.") || strings.HasPrefix(product.Model, "roborock.cd.") {
			return "zeo", nil
		}
		if strings.HasPrefix(product.Model, "roborock.wetdryvac.") {
			return "dyad", nil
		}
	case "B01":
		parts := strings.Split(product.Model, ".")
		model := parts[len(parts)-1]
		if strings.Contains(model, "ss") {
			return "q10", nil
		}
		if strings.Contains(model, "sc") {
			return "q7", nil
		}
	}
	return "", fmt.Errorf("unsupported device protocol %q for %s", device.PV, product.Model)
}

func (m *MQTTClient) ConfigureDevice(device Device, product Product) error {
	family, err := Family(device, product)
	if err != nil {
		return err
	}
	m.family, m.device, m.product = family, device, product
	m.SetDeviceKey(device.LocalKey)
	return nil
}

func Datapoints(family string) map[string]string { return reference.Datapoints[family] }

func DatapointCode(family, value string) (string, error) {
	if code, err := strconv.Atoi(value); err == nil && code > 0 && code <= 65535 {
		return strconv.Itoa(code), nil
	}
	for code, name := range Datapoints(family) {
		if strings.EqualFold(name, value) {
			return code, nil
		}
	}
	return "", fmt.Errorf("unknown %s datapoint %q; use datapoints list", family, value)
}

func (m *MQTTClient) dispatchFamily(msg *Message) {
	if msg.Protocol == ProtoMapResponse {
		select {
		case m.mapUpdates <- msg.Payload:
		default:
		}
		return
	}
	raw := msg.Payload
	if m.family == "q7" {
		if unpadded, err := pkcs7Unpad(raw, aes.BlockSize); err == nil {
			raw = unpadded
		}
	}
	var outer struct {
		DPS map[string]json.RawMessage `json:"dps"`
	}
	if json.Unmarshal(raw, &outer) != nil || len(outer.DPS) == 0 {
		return
	}
	if m.family == "q10" && outer.DPS["101"] != nil {
		var common map[string]json.RawMessage
		if json.Unmarshal(outer.DPS["101"], &common) != nil {
			return
		}
		delete(outer.DPS, "101")
		for key, value := range common {
			outer.DPS[key] = value
		}
	}
	m.stream.push(outer.DPS, m.family)
	select {
	case m.updates <- outer.DPS:
	default:
	}
}

func (m *MQTTClient) publishFamily(ctx context.Context, duid string, points map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.client == nil {
		return fmt.Errorf("mqtt not connected")
	}
	payload, err := json.Marshal(map[string]any{"dps": points, "t": nowTimestamp()})
	if err != nil {
		return err
	}
	version := "B01"
	if m.family == "dyad" || m.family == "zeo" {
		version = "A01"
	}
	// Q7 pads its JSON before the B01 codec adds the outer CBC padding.
	if m.family == "q7" {
		payload = pkcs7Pad(payload, aes.BlockSize)
	}
	frame, err := EncodeMQTTFrame(&Message{Version: version, Seq: uint32(RandomInt(100000, 999999)), Random: uint32(RandomInt(10000, 99999)), Timestamp: nowTimestamp(), Protocol: ProtoRPCRequest, Payload: payload}, m.device.LocalKey)
	if err != nil {
		return err
	}
	token := m.client.Publish(m.publishTopic(duid), 1, false, frame)
	if !token.WaitTimeout(m.Timeout) {
		return fmt.Errorf("mqtt publish timed out")
	}
	return token.Error()
}

func (m *MQTTClient) callFamily(ctx context.Context, duid, method string, params any) (json.RawMessage, error) {
	m.familyMu.Lock()
	defer m.familyMu.Unlock()
	for len(m.updates) > 0 {
		<-m.updates
	}
	if method == CmdAppSegmentClean {
		return m.cleanFamilyRooms(ctx, duid, params)
	}
	if method == "set_clean_motor_mode" {
		return m.setFamilyMode(ctx, duid, params)
	}
	if m.family == "q7" {
		return m.callQ7(ctx, duid, method, params)
	}
	if method == CmdGetStatus {
		if m.family == "q10" {
			return m.queryPoints(ctx, duid, "102", map[string]any{}, []string{"121", "122"})
		}
		codes := []string{"201", "209", "216"}
		if m.family == "zeo" {
			codes = []string{"203", "217", "220"}
		}
		return m.queryPoints(ctx, duid, "10000", pointInts(codes), codes)
	}
	if method == "get_mode_status" && m.family == "q10" {
		return m.queryPoints(ctx, duid, "102", map[string]any{}, []string{"137", "123", "124"})
	}
	if method == "datapoints.get" {
		return m.getFamilyPoints(ctx, duid, params)
	}
	if m.family == "q10" {
		if method == CmdGetCleanSummary {
			return m.queryPoints(ctx, duid, "101", map[string]any{"52": map[string]string{"op": "list"}}, []string{"52"})
		}
		if method == CmdGetMultiMapsList {
			return m.queryPoints(ctx, duid, "101", map[string]any{"61": map[string]string{"op": "list"}}, []string{"61"})
		}
		commands := map[string]struct {
			code  string
			value int
		}{CmdAppStart: {"201", 1}, CmdAppPause: {"204", 0}, CmdAppStop: {"206", 0}, CmdAppCharge: {"202", 5}, CmdAppSpot: {"201", 5}, CmdAppStartCollectDust: {"203", 2}, CmdFindMe: {"11", 1}}
		if command, ok := commands[method]; ok {
			method, params = command.code, command.value
		}
	}
	code, err := DatapointCode(m.family, method)
	if err != nil {
		return nil, err
	}
	if params == nil {
		return nil, fmt.Errorf("datapoint writes require an explicit JSON value")
	}
	if err := m.publishFamily(ctx, duid, map[string]any{code: params}); err != nil {
		return nil, err
	}
	return json.RawMessage(`{"published":true,"verified":false}`), nil
}

func (m *MQTTClient) setFamilyMode(ctx context.Context, duid string, params any) (json.RawMessage, error) {
	var settings []map[string]int
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(raw, &settings) != nil || len(settings) != 1 {
		return nil, fmt.Errorf("invalid mode settings")
	}
	if m.family == "q7" {
		return m.callQ7(ctx, duid, "prop.set", settings[0])
	}
	if m.family != "q10" {
		return nil, fmt.Errorf("%s has no vacuum mode", m.family)
	}
	for _, key := range []string{"137", "123", "124", "78"} {
		if value, ok := settings[0][key]; ok {
			if err := m.publishFamily(ctx, duid, map[string]any{key: value}); err != nil {
				return nil, err
			}
		}
	}
	return json.RawMessage(`{"published":true,"verified":false}`), nil
}

func (m *MQTTClient) getFamilyPoints(ctx context.Context, duid string, params any) (json.RawMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	var codes []string
	if err := json.Unmarshal(raw, &codes); err != nil || len(codes) == 0 {
		return nil, fmt.Errorf("datapoints.get expects a list of codes")
	}
	for i, code := range codes {
		resolved, err := DatapointCode(m.family, code)
		if err != nil {
			return nil, err
		}
		codes[i] = resolved
	}
	if m.family == "q10" {
		return m.queryPoints(ctx, duid, "102", map[string]any{}, codes)
	}
	return m.queryPoints(ctx, duid, "10000", pointInts(codes), codes)
}

func pointInts(codes []string) []int {
	out := make([]int, len(codes))
	for i, code := range codes {
		out[i], _ = strconv.Atoi(code)
	}
	return out
}

func (m *MQTTClient) queryPoints(ctx context.Context, duid, code string, value any, wanted []string) (json.RawMessage, error) {
	if m.family == "dyad" || m.family == "zeo" {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		value = string(raw)
	}
	if err := m.publishFamily(ctx, duid, map[string]any{code: value}); err != nil {
		return nil, err
	}
	timer := time.NewTimer(m.Timeout)
	defer timer.Stop()
	result := map[string]json.RawMessage{}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, fmt.Errorf("timeout waiting for %s data points %v", m.family, wanted)
		case points := <-m.updates:
			for key, value := range points {
				result[key] = value
			}
			complete := true
			for _, key := range wanted {
				if result[key] == nil {
					complete = false
				}
			}
			if complete {
				return json.Marshal(result)
			}
		}
	}
}

func (m *MQTTClient) callQ7(ctx context.Context, duid, method string, params any) (json.RawMessage, error) {
	switch method {
	case "get_mode_status":
		method = "prop.get"
		params = map[string]any{"property": []string{"mode", "wind", "water", "clean_path_preference"}}
	case CmdGetStatus:
		method = "prop.get"
		params = map[string]any{"property": []string{"status", "fault", "wind", "water", "mode", "quantity", "work_mode", "charge_state", "cleaning_time", "cleaning_area", "current_map_id"}}
	case CmdAppStart, CmdAppPause, CmdAppStop:
		control := map[string]int{CmdAppStart: 1, CmdAppPause: 2, CmdAppStop: 0}[method]
		method = "service.set_room_clean"
		params = map[string]any{"clean_type": 0, "ctrl_value": control, "room_ids": []int{}}
	case CmdAppCharge:
		method = "service.start_recharge"
		params = map[string]any{}
	case CmdFindMe:
		method = "service.find_device"
		params = map[string]any{}
	case CmdGetMultiMapsList:
		method = "service.get_map_list"
		params = map[string]any{}
	case CmdGetCleanSummary:
		method = "service.get_record_list"
		params = map[string]any{}
	}
	if !strings.HasPrefix(method, "prop.") && !strings.HasPrefix(method, "service.") {
		return nil, fmt.Errorf("%s is not a Q7 command; use prop.get, prop.set, or service methods", method)
	}
	if params == nil {
		params = []any{}
	}
	id := strconv.FormatInt(100000000000+time.Now().UnixNano()%900000000000, 10)
	if err := m.publishFamily(ctx, duid, map[string]any{"10000": map[string]any{"method": method, "msgId": id, "params": params}}); err != nil {
		return nil, err
	}
	timer := time.NewTimer(m.Timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, fmt.Errorf("timeout waiting for Q7 %s", method)
		case points := <-m.updates:
			for _, point := range points {
				raw, err := dpsValue(point)
				if err != nil {
					continue
				}
				var response struct {
					ID   string          `json:"msgId"`
					Code int             `json:"code"`
					Data json.RawMessage `json:"data"`
				}
				if json.Unmarshal(raw, &response) != nil || response.ID != id {
					continue
				}
				if response.Code != 0 {
					return nil, fmt.Errorf("Q7 command rejected with code %d", response.Code)
				}
				if len(response.Data) == 0 {
					return nil, fmt.Errorf("Q7 response has no data")
				}
				return response.Data, nil
			}
		}
	}
}
