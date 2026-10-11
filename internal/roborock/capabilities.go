package roborock

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strings"
)

//go:embed reference.json
var referenceJSON []byte

var reference struct {
	Models map[string][]string `json:"models"`
	Rules  map[string]struct {
		Source string
		Mask   string
		ID     int
	} `json:"rules"`
	Docks         map[string]map[string]bool   `json:"docks"`
	FinishReasons map[string]string            `json:"finish_reasons"`
	StartTypes    map[string]string            `json:"start_types"`
	CleanTypes    map[string]string            `json:"clean_types"`
	Datapoints    map[string]map[string]string `json:"datapoints"`
}

func init() {
	if err := json.Unmarshal(referenceJSON, &reference); err != nil {
		panic(err)
	}
}

type Capabilities struct {
	Family     string          `json:"family"`
	Model      string          `json:"model"`
	KnownModel bool            `json:"known_model"`
	Features   map[string]bool `json:"features"`
	Dock       map[string]bool `json:"dock"`
	Modes      []string        `json:"modes"`
	Fan        map[string]int  `json:"fan"`
	Water      map[string]int  `json:"water"`
	Routes     map[string]int  `json:"routes"`
}

// DecodeCapabilities combines model hardware with the device's current feature flags.
func DecodeCapabilities(model, region string, initRaw, statusRaw json.RawMessage) (Capabilities, error) {
	c := Capabilities{Family: "v1", Model: model, Features: map[string]bool{}, Fan: map[string]int{"quiet": 101, "balanced": 102, "turbo": 103, "max": 104}, Water: map[string]int{}, Routes: map[string]int{}, Modes: []string{"vacuum"}}
	parts := strings.Split(model, ".")
	features, known := reference.Models[parts[len(parts)-1]]
	c.KnownModel = known
	for _, name := range features {
		c.Features[name] = true
	}
	initData, err := Object(initRaw)
	if err != nil {
		return c, err
	}
	status, err := Object(statusRaw)
	if err != nil {
		return c, err
	}
	if err := c.readFeatureFlags(initData); err != nil {
		return c, err
	}
	f := c.Features
	f["is_clean_route_setting_supported"] = f["is_clean_route_setting_supported"] || f["is_shake_mop_set_supported"] || f["is_roller_mop_supported"]
	var dock, fan int
	_ = json.Unmarshal(status["dock_type"], &dock)
	_ = json.Unmarshal(status["fan_power"], &fan)
	c.Dock = reference.Docks[fmt.Sprint(dock)]
	if fan > 0 && fan < 100 {
		c.Fan = map[string]int{"quiet": 38, "balanced": 60, "turbo": 75, "max": 100}
	}
	if f["is_max_plus_mode_supported"] || f["is_none_pure_clean_mop_with_max_plus"] {
		c.Fan["max_plus"] = 108
	}
	if f["is_pure_clean_mop_supported"] {
		c.Fan["off"] = 105
	} else if fan >= 100 {
		c.Fan["gentle"] = 105
	}
	c.setWaterModes()
	c.setRoutes(region)
	for _, mode := range []struct {
		name, feature     string
		fan, water, route int
	}{{"custom", "is_customized_clean_supported", 106, 204, 302}, {"smart_mode", "is_smart_clean_mode_set_supported", 110, 209, 306}} {
		if !f[mode.feature] {
			continue
		}
		c.Fan[mode.name] = mode.fan
		if len(c.Water) > 0 && !f["is_water_slide_mode_supported"] {
			c.Water[mode.name] = mode.water
			c.Modes = append(c.Modes, mode.name)
		}
		if len(c.Routes) > 0 && !f["is_none_pure_clean_mop_with_max_plus"] {
			c.Routes[mode.name] = mode.route
		}
	}
	return c, nil
}

func (c *Capabilities) readFeatureFlags(initData map[string]json.RawMessage) error {
	var flags []int
	if raw := initData["feature_info"]; raw != nil {
		if err := json.Unmarshal(raw, &flags); err != nil {
			return err
		}
	}
	for name, rule := range reference.Rules {
		if rule.Source == "feature_info" {
			c.Features[name] = slices.Contains(flags, rule.ID)
			continue
		}
		value := strings.Trim(string(initData[rule.Source]), "\"")
		if value == "" {
			continue
		}
		base := 10
		if rule.Source == "new_feature_info_str" {
			base = 16
		}
		bits, ok := new(big.Int).SetString(value, base)
		if !ok || bits.Sign() < 0 {
			return fmt.Errorf("invalid %s", rule.Source)
		}
		mask, _ := new(big.Int).SetString(rule.Mask, 10)
		c.Features[name] = new(big.Int).And(bits, mask).Sign() != 0
	}
	return nil
}

func (c *Capabilities) setWaterModes() {
	f := c.Features
	if !f["is_support_water_mode"] {
		return
	}
	c.Modes = append(c.Modes, "vac_and_mop")
	c.Water = map[string]int{"off": 200, "low": 201, "medium": 202, "high": 203}
	if f["is_mop_shake_module_supported"] {
		c.Water = map[string]int{"off": 200, "mild": 201, "standard": 202, "intense": 203}
	}
	if f["is_custom_water_box_distance_supported"] {
		c.Water["custom_water_flow"] = 207
	}
	if f["is_mop_shake_module_supported"] && f["is_mop_shake_water_max_supported"] {
		c.Water["extreme"] = 208
	}
	if f["is_water_slide_mode_supported"] {
		c.Water = map[string]int{"off": 200, "slight": 221, "low": 225, "gentle": 230, "medium": 235, "moderate": 240, "high": 245, "extreme": 250}
	}
	if f["is_pure_clean_mop_supported"] {
		c.Modes = append(c.Modes, "mop")
	}
}

func (c *Capabilities) setRoutes(region string) {
	f := c.Features
	if !f["is_clean_route_setting_supported"] {
		return
	}
	c.Routes["standard"] = 300
	if f["is_none_pure_clean_mop_with_max_plus"] {
		c.Routes["fast"] = 304
		return
	}
	if !f["is_clean_efficiency_supported"] {
		c.Routes["deep"] = 301
	}
	if f["is_careful_slow_mop_supported"] {
		c.Routes["deep_plus"] = 303
		if !f["is_corner_clean_mode_supported"] && f["is_clean_route_deep_slow_plus_supported"] && region == "cn" {
			c.Routes["deep_plus"] = 305
		}
	}
	if f["is_clean_route_fast_mode_supported"] {
		c.Routes["fast"] = 304
	}
}

// Object unwraps the one-element array returned by many firmware methods.
func Object(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var list []json.RawMessage
	if len(raw) > 0 && raw[0] == '[' {
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
		if len(list) != 1 {
			return nil, fmt.Errorf("expected one device object, got %d", len(list))
		}
		raw = list[0]
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("device returned no object")
	}
	return out, nil
}

func (c Capabilities) ModeParameters(mode, fan, water, route string) (map[string]int, error) {
	if c.Family != "" && c.Family != "v1" {
		return c.familyModeParameters(mode, fan, water, route)
	}
	if !slices.Contains(c.Modes, mode) {
		return nil, fmt.Errorf("mode %q is not supported; use capabilities", mode)
	}
	params := map[string]int{"fan_power": c.Fan["balanced"]}
	if len(c.Water) > 0 {
		params["water_box_mode"] = 202
		if c.Features["is_water_slide_mode_supported"] {
			params["water_box_mode"] = 235
		}
	}
	if len(c.Routes) > 0 {
		params["mop_mode"] = 300
	}
	switch mode {
	case "vacuum":
		if len(c.Water) > 0 {
			params["water_box_mode"] = 200
		}
	case "mop":
		params["fan_power"] = 105
	case "custom", "smart_mode":
		params["fan_power"], params["water_box_mode"] = c.Fan[mode], c.Water[mode]
		if len(c.Routes) > 0 {
			params["mop_mode"] = c.Routes[mode]
		}
	}
	for _, option := range []struct {
		name, value, field string
		choices            map[string]int
	}{{"fan", fan, "fan_power", c.Fan}, {"water", water, "water_box_mode", c.Water}, {"route", route, "mop_mode", c.Routes}} {
		if option.value == "" {
			continue
		}
		code, ok := option.choices[option.value]
		if !ok {
			return nil, fmt.Errorf("unsupported %s %q; use capabilities", option.name, option.value)
		}
		params[option.field] = code
	}
	if mode == "vacuum" && len(c.Water) > 0 && params["water_box_mode"] != 200 {
		return nil, fmt.Errorf("vacuum mode requires water off")
	}
	if mode == "mop" && params["fan_power"] != 105 {
		return nil, fmt.Errorf("mop mode requires fan off")
	}
	if mode == "vac_and_mop" && (params["fan_power"] == 105 || params["water_box_mode"] == 200) {
		return nil, fmt.Errorf("vac_and_mop requires fan and water")
	}
	return params, nil
}

func FamilyCapabilities(family, model string) Capabilities {
	c := Capabilities{Family: family, Model: model, KnownModel: true, Features: map[string]bool{}, Dock: map[string]bool{}, Modes: []string{}}
	if family != "q7" && family != "q10" {
		return c
	}
	c.Modes = []string{"vacuum", "vac_and_mop", "mop"}
	c.Fan = map[string]int{"quiet": 1, "balanced": 2, "turbo": 3, "max": 4, "max_plus": 5}
	c.Water = map[string]int{"low": 1, "medium": 2, "high": 3}
	c.Routes = map[string]int{"balanced": 0, "deep": 1}
	if family == "q10" {
		c.Fan["off"] = 0
		c.Fan["max_plus"] = 8
		c.Water["off"] = 0
		c.Routes = map[string]int{"fast": 0, "daily": 1, "fine": 2}
	}
	return c
}

func (c Capabilities) familyModeParameters(mode, fan, water, route string) (map[string]int, error) {
	if !slices.Contains(c.Modes, mode) {
		return nil, fmt.Errorf("unsupported %s mode %q", c.Family, mode)
	}
	key := "mode"
	modes := map[string]int{"vacuum": 0, "vac_and_mop": 1, "mop": 2}
	fields := []string{"wind", "water", "clean_path_preference"}
	if c.Family == "q10" {
		key = "137"
		modes = map[string]int{"vacuum": 2, "vac_and_mop": 1, "mop": 3}
		fields = []string{"123", "124", "78"}
	}
	params := map[string]int{key: modes[mode]}
	for i, option := range []struct {
		value   string
		choices map[string]int
	}{{fan, c.Fan}, {water, c.Water}, {route, c.Routes}} {
		if option.value == "" {
			continue
		}
		value, ok := option.choices[option.value]
		if !ok {
			return nil, fmt.Errorf("unsupported mode option %q", option.value)
		}
		params[fields[i]] = value
	}
	if mode == "vacuum" && water != "" && water != "off" {
		return nil, fmt.Errorf("vacuum mode cannot request water flow")
	}
	if mode == "mop" && fan != "" && fan != "off" {
		return nil, fmt.Errorf("mop mode cannot request suction")
	}
	return params, nil
}
