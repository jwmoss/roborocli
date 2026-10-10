package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jwmoss/roborocli/internal/roborock"
)

func newDiscoverCommand(rc *runtime) *cobra.Command {
	var wait time.Duration
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Find vacuums announcing themselves on the local network",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			found, err := roborock.Discover(wait)
			if err != nil {
				return err
			}
			if rc.out.IsJSON() {
				return rc.out.JSON(map[string]any{"devices": found})
			}
			if len(found) == 0 {
				return fmt.Errorf("no vacuum announced itself within %s", wait)
			}
			rows := make([][]string, 0, len(found))
			for _, device := range found {
				rows = append(rows, []string{device.DUID, device.IP, device.Version})
			}
			rc.out.Table([]string{"DUID", "IP", "PV"}, rows)
			return nil
		},
	}
	cmd.Flags().DurationVar(&wait, "wait", 5*time.Second, "how long to listen")
	return cmd
}

func newDevicesCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:     "devices",
		Aliases: []string{"device"},
		Short:   "List vacuums in the account",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := rc.loadConfig()
			if err != nil {
				return err
			}
			if err := cfg.ValidateAuth(); err != nil {
				return err
			}
			cloud := rc.cloudClient(cfg)
			user := cfg.UserData
			homeID, err := cloud.HomeID(cmd.Context(), *user)
			if err != nil {
				return fmt.Errorf("fetch home id: %w", err)
			}
			home, err := cloud.HomeData(cmd.Context(), *user, homeID)
			if err != nil {
				return fmt.Errorf("fetch home data: %w", err)
			}
			devices := home.AllDevices()
			if rc.out.IsJSON() {
				type entry struct {
					roborock.Device
					Model string `json:"model,omitempty"`
					IP    string `json:"ip,omitempty"`
				}
				list := make([]entry, 0, len(devices))
				for _, d := range devices {
					list = append(list, entry{
						Device: d,
						Model:  home.ProductFor(d).Model,
						IP:     cfg.DeviceIPs[d.DUID],
					})
				}
				return rc.out.JSON(map[string]any{"devices": list, "home": home.Name})
			}
			rows := make([][]string, 0, len(devices))
			for _, d := range devices {
				product := home.ProductFor(d)
				online := "unknown"
				if d.Online != nil {
					online = strconv.FormatBool(*d.Online)
				}
				rows = append(rows, []string{
					d.DUID,
					d.Name,
					product.Model,
					d.PV,
					d.FV,
					online,
					cfg.DeviceIPs[d.DUID],
				})
			}
			rc.out.Table([]string{"DUID", "NAME", "MODEL", "PV", "FW", "ONLINE", "IP"}, rows)
			return nil
		},
	}
}

func newStatusCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Aliases: []string{"state"},
		Short:   "Show the current vacuum status",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			session, err := rc.openSession(cmd.Context())
			if err != nil {
				return err
			}
			caller, callErr := rc.openCaller(cmd.Context(), session)
			if callErr == nil {
				defer caller.Close()
				result, err := caller.Call(cmd.Context(), roborock.CmdGetStatus, nil)
				if err == nil {
					if rc.out.IsJSON() {
						var pretty any
						if json.Unmarshal(result, &pretty) == nil {
							return rc.out.JSON(map[string]any{
								"device":    session.Device.Name,
								"duid":      session.Device.DUID,
								"transport": caller.Transport(),
								"status":    pretty,
							})
						}
					}
					return rc.renderStatus(result, caller.Transport())
				}
				callErr = err
			}
			shadow, err := session.Cloud.DeviceShadow(cmd.Context(), session.User.RRiot, session.Device.DUID)
			if err != nil {
				return callErr
			}
			if rc.out.IsJSON() {
				var pretty any
				json.Unmarshal(shadow, &pretty)
				return rc.out.JSON(map[string]any{
					"device":    session.Device.Name,
					"duid":      session.Device.DUID,
					"transport": "cloud-shadow",
					"status":    pretty,
					"warning":   callErr.Error(),
				})
			}
			return rc.renderShadow(shadow)
		},
	}
}

func (rc *runtime) renderShadow(raw json.RawMessage) error {
	var points map[string]any
	if err := json.Unmarshal(raw, &points); err != nil {
		return fmt.Errorf("decode device shadow: %w", err)
	}
	state := asInt(points["121"])
	battery := asInt(points["122"])
	fan := asInt(points["123"])
	water := asInt(points["124"])
	errorCode := asInt(points["120"])
	rows := [][]string{
		{"state", fmt.Sprintf("%d (%s)", state, roborock.StateName(state))},
		{"battery", fmt.Sprintf("%d%%", battery)},
		{"transport", "cloud-shadow"},
	}
	if name := roborock.FanSpeedName(fan); name != "" {
		rows = append(rows, []string{"fan_power", fmt.Sprintf("%d (%s)", fan, name)})
	}
	if name := roborock.MopModeName(water); name != "" {
		rows = append(rows, []string{"water_box_mode", fmt.Sprintf("%d (%s)", water, name)})
	}
	if errorCode != 0 {
		rows = append(rows, []string{"error", fmt.Sprintf("%d (%s)", errorCode, roborock.ErrorName(errorCode))})
	}
	for _, item := range []struct {
		code, label string
	}{
		{"125", "main_brush"},
		{"126", "side_brush"},
		{"127", "filter"},
		{"133", "charge_status"},
		{"134", "drying_status"},
	} {
		if value, ok := points[item.code]; ok {
			rows = append(rows, []string{item.label, fmt.Sprintf("%v", value)})
		}
	}
	rc.out.Table([]string{"FIELD", "VALUE"}, rows)
	return nil
}

func (rc *runtime) renderStatus(result json.RawMessage, transport string) error {
	status, err := statusObject(result)
	if err != nil {
		return err
	}
	state := asInt(status["state"])
	battery := asInt(status["battery"])
	fanPower := asInt(status["fan_power"])
	errorCode := asInt(status["error_code"])

	cleanTime := asInt(status["clean_time"])
	cleanArea := asInt(status["clean_area"])
	rows := [][]string{
		{"state", fmt.Sprintf("%d (%s)", state, roborock.StateName(state))},
		{"battery", fmt.Sprintf("%d%%", battery)},
	}
	if name := roborock.FanSpeedName(fanPower); name != "" {
		rows = append(rows, []string{"fan_power", fmt.Sprintf("%d (%s)", fanPower, name)})
	}
	if mop := asInt(status["water_box_mode"]); mop > 0 {
		if name := roborock.MopModeName(mop); name != "" {
			rows = append(rows, []string{"water_box_mode", fmt.Sprintf("%d (%s)", mop, name)})
		}
	}
	if errorCode != 0 {
		rows = append(rows, []string{"error", fmt.Sprintf("%d (%s)", errorCode, roborock.ErrorName(errorCode))})
	}
	if cleanTime > 0 {
		rows = append(rows, []string{"clean_time", formatSeconds(cleanTime)})
	}
	if cleanArea > 0 {
		rows = append(rows, []string{"clean_area", fmt.Sprintf("%d cm2", cleanArea)})
	}
	if msg, ok := status["msg_status"].(map[string]any); ok && len(msg) > 0 {
		rows = append(rows, []string{"msg_status", fmt.Sprintf("%v", msg)})
	}
	if rc.g.traceHTTP || transport != "" {
		rows = append(rows, []string{"transport", transport})
	}
	if rc.out.IsPlain() {
		for _, row := range rows {
			rc.out.Printf("%s=%s\n", row[0], row[1])
		}
		return nil
	}
	rc.out.Table([]string{"FIELD", "VALUE"}, rows)
	return nil
}

func newInfoCommand(rc *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Show device and maintenance information",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
				ctx := cmd.Context()
				results := map[string]json.RawMessage{}
				for _, method := range []string{
					roborock.CmdGetStatus,
					roborock.CmdGetConsumable,
					roborock.CmdGetNetworkInfo,
					roborock.CmdGetRoomMapping,
					roborock.CmdGetCleanSummary,
				} {
					res, err := caller.Call(ctx, method, nil)
					if err != nil {
						continue // not every firmware supports every command
					}
					results[method] = res
				}
				payload := map[string]any{
					"device":    session.Device,
					"product":   session.Product,
					"transport": caller.Transport(),
				}
				for method, res := range results {
					var pretty any
					if err := json.Unmarshal(res, &pretty); err == nil {
						payload[method] = pretty
					} else {
						payload[method] = json.RawMessage(res)
					}
				}
				if rc.out.IsJSON() || rc.out.IsPlain() {
					return rc.out.JSON(payload)
				}
				return rc.renderInfo(session, payload)
			})
		},
	}

	cmd.AddCommand(newConsumablesCommand(rc))
	cmd.AddCommand(newRoomsCommand(rc))
	cmd.AddCommand(newNetworkCommand(rc))
	cmd.AddCommand(newHistoryCommand(rc))
	return cmd
}

func (rc *runtime) renderInfo(session *deviceSession, payload map[string]any) error {
	rows := [][]string{
		{"name", session.Device.Name},
		{"duid", session.Device.DUID},
		{"model", session.Product.Model},
		{"firmware", session.Device.FV},
		{"protocol", session.Device.PV},
		{"serial", session.Device.SN},
	}
	if netInfo, ok := payload[roborock.CmdGetNetworkInfo].(map[string]any); ok {
		rows = append(rows,
			[]string{"ip", fmt.Sprintf("%v", netInfo["ip"])},
			[]string{"ssid", fmt.Sprintf("%v", netInfo["ssid"])},
			[]string{"mac", fmt.Sprintf("%v", netInfo["mac"])},
		)
	}
	if consumable, ok := payload[roborock.CmdGetConsumable].(map[string]any); ok {
		for _, part := range []string{"main_brush_work_time", "side_brush_work_time", "filter_work_time"} {
			if value, ok := consumable[part]; ok {
				rows = append(rows, []string{part, formatSeconds(asInt(value))})
			}
		}
	}
	rc.out.Table([]string{"FIELD", "VALUE"}, rows)
	return nil
}

func newConsumablesCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "consumables",
		Short: "Show brush, filter, and dock part lifetimes",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
				res, err := caller.Call(cmd.Context(), roborock.CmdGetConsumable, nil)
				if err != nil {
					return err
				}
				var consumable map[string]any
				if err := json.Unmarshal(res, &consumable); err != nil {
					return fmt.Errorf("decode consumables: %w", err)
				}
				if rc.out.IsJSON() || rc.out.IsPlain() {
					return rc.out.JSON(consumable)
				}
				rows := make([][]string, 0, len(consumable))
				for key, value := range consumable {
					if secs := asInt(value); secs > 0 {
						rows = append(rows, []string{key, formatSeconds(secs)})
						continue
					}
					rows = append(rows, []string{key, fmt.Sprintf("%v", value)})
				}
				rc.out.Table([]string{"PART", "VALUE"}, rows)
				return nil
			})
		},
	}
}

func newRoomsCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "rooms",
		Short: "List rooms from the device map",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
				res, err := caller.Call(cmd.Context(), roborock.CmdGetRoomMapping, nil)
				if err != nil {
					return err
				}
				var mapping []struct {
					ID   int64  `json:"id"`
					Name string `json:"name"`
				}
				if err := json.Unmarshal(res, &mapping); err != nil {
					// Some firmware returns an object keyed by room id.
					var byID map[string]string
					if err2 := json.Unmarshal(res, &byID); err2 != nil {
						return fmt.Errorf("decode rooms: %w", err)
					}
					for id, name := range byID {
						parsed, _ := strconv.ParseInt(id, 10, 64)
						mapping = append(mapping, struct {
							ID   int64  `json:"id"`
							Name string `json:"name"`
						}{parsed, name})
					}
				}
				if rc.out.IsJSON() {
					return rc.out.JSON(mapping)
				}
				rows := make([][]string, 0, len(mapping))
				for _, room := range mapping {
					rows = append(rows, []string{strconv.FormatInt(room.ID, 10), room.Name})
				}
				rc.out.Table([]string{"ID", "NAME"}, rows)
				return nil
			})
		},
	}
}

func newNetworkCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "network",
		Short: "Show the device network details and refresh its cached IP",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
				res, err := caller.Call(cmd.Context(), roborock.CmdGetNetworkInfo, nil)
				if err != nil {
					return err
				}
				var info roborock.NetworkInfo
				if err := json.Unmarshal(res, &info); err != nil {
					return fmt.Errorf("decode network info: %w", err)
				}
				if info.IP != "" && !rc.g.dryRun && rc.cfg.DeviceIPs != nil {
					if rc.cfg.DeviceIPs[session.Device.DUID] != info.IP {
						next := *rc.cfg
						next.DeviceIPs[session.Device.DUID] = info.IP
						if err := configSave(rc, next); err != nil {
							rc.out.Errorf("cache device IP: %v", err)
						}
					}
				}
				if rc.out.IsJSON() || rc.out.IsPlain() {
					return rc.out.JSON(info)
				}
				rc.out.Table([]string{"FIELD", "VALUE"}, [][]string{
					{"ip", info.IP},
					{"ssid", info.SSID},
					{"mac", info.MAC},
					{"bssid", info.BSSID},
					{"rssi", strconv.Itoa(info.RSSI)},
				})
				return nil
			})
		},
	}
}

func newHistoryCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:     "history",
		Aliases: []string{"clean-history"},
		Short:   "Show the cleaning summary",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
				res, err := caller.Call(cmd.Context(), roborock.CmdGetCleanSummary, nil)
				if err != nil {
					return err
				}
				if rc.out.IsJSON() || rc.out.IsPlain() {
					var pretty any
					json.Unmarshal(res, &pretty)
					return rc.out.JSON(pretty)
				}
				var summary map[string]any
				if err := json.Unmarshal(res, &summary); err != nil {
					return fmt.Errorf("decode clean summary: %w", err)
				}
				rows := [][]string{
					{"total_clean_time", formatSeconds(asInt(summary["total_clean_time"]))},
					{"total_clean_area", fmt.Sprintf("%v cm2", summary["total_clean_area"])},
					{"total_clean_count", fmt.Sprintf("%v", summary["total_clean_count"])},
					{"last_clean_t", formatSeconds(asInt(summary["last_clean_t"]))},
				}
				rc.out.Table([]string{"FIELD", "VALUE"}, rows)
				return nil
			})
		},
	}
}

func newSettingsCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "settings",
		Short: "Show device settings that the firmware exposes",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
				ctx := cmd.Context()
				readers := []struct {
					name   string
					method string
				}{
					{"custom_mode", roborock.CmdGetCustomMode},
					{"sound_volume", roborock.CmdGetSoundVolume},
					{"child_lock", roborock.CmdGetChildLockStatus},
					{"led_status", roborock.CmdGetLedStatus},
					{"carpet_mode", roborock.CmdGetCarpetMode},
					{"collision_avoid", roborock.CmdGetCollisionAvoidStatus},
					{"dnd_timer", roborock.CmdGetDndTimer},
					{"map_status", roborock.CmdGetMapStatus},
					{"dock_info", roborock.CmdGetDockInfo},
				}
				payload := map[string]any{}
				for _, reader := range readers {
					res, err := caller.Call(ctx, reader.method, nil)
					if err != nil {
						continue
					}
					var pretty any
					if err := json.Unmarshal(res, &pretty); err == nil {
						payload[reader.name] = pretty
					} else {
						payload[reader.name] = json.RawMessage(res)
					}
				}
				if len(payload) == 0 {
					return fmt.Errorf("device returned no settings; firmware may not support them")
				}
				if rc.out.IsJSON() || rc.out.IsPlain() {
					return rc.out.JSON(payload)
				}
				rows := make([][]string, 0, len(payload))
				for _, name := range []string{"custom_mode", "sound_volume", "child_lock", "led_status",
					"carpet_mode", "collision_avoid", "dnd_timer", "map_status", "dock_info"} {
					value, ok := payload[name]
					if !ok {
						continue
					}
					rows = append(rows, []string{name, compact(value)})
				}
				rc.out.Table([]string{"SETTING", "VALUE"}, rows)
				return nil
			})
		},
	}
}

// newControlCommand groups the vacuum control verbs.
func newControlCommand(rc *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "control",
		Short: "Control the vacuum",
	}
	cmd.AddCommand(
		newSimpleControl(rc, "start", "Start or resume cleaning", roborock.CmdAppStart, nil),
		newSimpleControl(rc, "pause", "Pause cleaning", roborock.CmdAppPause, nil),
		newSimpleControl(rc, "stop", "Stop cleaning", roborock.CmdAppStop, nil),
		newSimpleControl(rc, "charge", "Return to the dock", roborock.CmdAppCharge, nil),
		newSimpleControl(rc, "spot", "Start a spot clean", roborock.CmdAppSpot, nil),
		newSimpleControl(rc, "find", "Locate the vacuum", roborock.CmdFindMe, nil),
		newSimpleControl(rc, "resume-map", "Resume map building", roborock.CmdAppResumeBuildMap, nil),
	)
	cmd.AddCommand(newCleanRoomsCommand(rc))
	cmd.AddCommand(newGotoCommand(rc))
	cmd.AddCommand(newFanCommand(rc))
	cmd.AddCommand(newWaterCommand(rc))
	cmd.AddCommand(newVolumeCommand(rc))
	cmd.AddCommand(newDndCommand(rc))
	cmd.AddCommand(newResetConsumableCommand(rc))
	return cmd
}

// newSimpleControl builds a verb that sends one method with no parameters.
func newSimpleControl(rc *runtime, name, short, method string, params any) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return rc.runMutating(cmd, method, params, fmt.Sprintf("%s sent", method))
		},
	}
}

// runMutating refuses mutation under --dry-run, then sends the method.
func (rc *runtime) runMutating(cmd *cobra.Command, method string, params any, successMsg string) error {
	if rc.g.dryRun {
		return fmt.Errorf("dry-run: refusing to send %s", method)
	}
	return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
		res, err := caller.Call(cmd.Context(), method, params)
		if err != nil {
			return err
		}
		if rc.out.IsJSON() {
			var pretty any
			json.Unmarshal(res, &pretty)
			return rc.out.JSON(map[string]any{
				"method":    method,
				"result":    pretty,
				"transport": caller.Transport(),
			})
		}
		rc.out.Success(successMsg)
		if rc.out.IsPlain() {
			rc.out.Printf("%s\n", method)
		}
		return nil
	})
}

func newCleanRoomsCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "rooms <id> [id...]",
		Short: "Clean only the given room ids",
		Args:  usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids := make([]int, 0, len(args))
			for _, arg := range args {
				value, err := strconv.Atoi(arg)
				if err != nil {
					return fmt.Errorf("%w: room id %q is not a number", errUsage, arg)
				}
				ids = append(ids, value)
			}
			return rc.runMutating(cmd, roborock.CmdAppSegmentClean, ids, "room cleaning started")
		},
	}
}

func newGotoCommand(rc *runtime) *cobra.Command {
	var repeat int
	cmd := &cobra.Command{
		Use:   "goto <x_mm> <y_mm>",
		Short: "Drive the vacuum to a map coordinate in millimeters",
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("%w: x %q is not a number", errUsage, args[0])
			}
			y, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("%w: y %q is not a number", errUsage, args[1])
			}
			if repeat > 1 {
				return rc.runMutating(cmd, roborock.CmdAppGotoTarget,
					[]any{x, y, repeat}, "goto sent")
			}
			return rc.runMutating(cmd, roborock.CmdAppGotoTarget, []any{x, y}, "goto sent")
		},
	}
	cmd.Flags().IntVar(&repeat, "repeat", 1, "repeat count")
	return cmd
}

func newFanCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "fan <speed>",
		Short: "Set the fan speed (get without arguments)",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return rc.showSetting(cmd, roborock.CmdGetCustomMode)
			}
			speed, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("%w: fan speed %q is not a number", errUsage, args[0])
			}
			if rc.g.dryRun {
				return fmt.Errorf("dry-run: refusing to set fan speed")
			}
			return rc.runMutating(cmd, roborock.CmdSetCustomMode, []any{speed}, "fan speed set")
		},
	}
}

func newWaterCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "water <mode>",
		Short: "Set the water box mode (get without arguments)",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return rc.showSetting(cmd, roborock.CmdGetWaterBoxCustomMode)
			}
			mode, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("%w: water mode %q is not a number", errUsage, args[0])
			}
			if rc.g.dryRun {
				return fmt.Errorf("dry-run: refusing to set water mode")
			}
			return rc.runMutating(cmd, roborock.CmdSetWaterBoxCustomMode, []any{mode}, "water mode set")
		},
	}
}

func newVolumeCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "volume <0-100>",
		Short: "Set the speaker volume (get without arguments)",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return rc.showSetting(cmd, roborock.CmdGetSoundVolume)
			}
			value, err := strconv.Atoi(args[0])
			if err != nil || value < 0 || value > 100 {
				return fmt.Errorf("%w: volume %q must be 0-100", errUsage, args[0])
			}
			if rc.g.dryRun {
				return fmt.Errorf("dry-run: refusing to set volume")
			}
			return rc.runMutating(cmd, roborock.CmdSetSoundVolume, []any{value}, "volume set")
		},
	}
}

func newDndCommand(rc *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dnd",
		Short: "Show or change the do-not-disturb timer",
		Args:  usageArgs(cobra.MaximumNArgs(3)),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return rc.showSetting(cmd, roborock.CmdGetDndTimer)
			case args[0] == "off":
				if rc.g.dryRun {
					return fmt.Errorf("dry-run: refusing to close the DND timer")
				}
				return rc.runMutating(cmd, roborock.CmdCloseDndTimer, nil, "DND timer closed")
			case len(args) == 3:
				start, err := parseHour(args[1])
				if err != nil {
					return err
				}
				end, err := parseHour(args[2])
				if err != nil {
					return err
				}
				if rc.g.dryRun {
					return fmt.Errorf("dry-run: refusing to set the DND timer")
				}
				return rc.runMutating(cmd, roborock.CmdSetDndTimer,
					[]any{start, end}, "DND timer set")
			default:
				return fmt.Errorf("%w: use dnd, dnd off, or dnd set <start HH:MM> <end HH:MM>", errUsage)
			}
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "off",
		Short: "Turn the do-not-disturb timer off",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if rc.g.dryRun {
				return fmt.Errorf("dry-run: refusing to close the DND timer")
			}
			return rc.runMutating(cmd, roborock.CmdCloseDndTimer, nil, "DND timer closed")
		},
	})
	return cmd
}

// parseHour parses an "HH:MM" clock value into minutes since midnight.
func parseHour(value string) (int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("%w: time %q must be HH:MM", errUsage, value)
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, fmt.Errorf("%w: hour %q must be 0-23", errUsage, parts[0])
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("%w: minute %q must be 0-59", errUsage, parts[1])
	}
	return hour*60 + minute, nil
}

func newResetConsumableCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "reset-consumable <main_brush|side_brush|filter|mop_roller|dust_bag>",
		Short: "Reset the lifetime counter for one part",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			allowed := map[string]bool{
				"main_brush": true, "side_brush": true, "filter": true,
				"mop_roller": true, "dust_bag": true, "sensor_dirty": true,
			}
			if !allowed[args[0]] {
				return fmt.Errorf("%w: unknown part %q", errUsage, args[0])
			}
			if rc.g.dryRun {
				return fmt.Errorf("dry-run: refusing to reset %s", args[0])
			}
			return rc.runMutating(cmd, roborock.CmdResetConsumable,
				[]any{args[0]}, args[0]+" counter reset")
		},
	}
}

// showSetting prints one read-only setting.
func (rc *runtime) showSetting(cmd *cobra.Command, method string) error {
	return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
		res, err := caller.Call(cmd.Context(), method, nil)
		if err != nil {
			return err
		}
		if rc.out.IsJSON() || rc.out.IsPlain() {
			var pretty any
			json.Unmarshal(res, &pretty)
			return rc.out.JSON(pretty)
		}
		rc.out.Printf("%s\n", compact(prettyOrRaw(res)))
		return nil
	})
}

// prettyOrRaw unmarshals JSON when possible and falls back to raw bytes.
func prettyOrRaw(raw json.RawMessage) any {
	var pretty any
	if err := json.Unmarshal(raw, &pretty); err != nil {
		return json.RawMessage(raw)
	}
	return pretty
}

// statusObject accepts the object form and the one-element array form
// returned by get_status.
func statusObject(raw json.RawMessage) (map[string]any, error) {
	var status map[string]any
	if err := json.Unmarshal(raw, &status); err == nil {
		return status, nil
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err == nil && len(list) > 0 {
		return list[0], nil
	}
	return nil, fmt.Errorf("decode status: unexpected payload")
}

// asInt coerces a JSON number to int, accepting strings too.
func asInt(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return 0
}

// formatSeconds renders seconds as a compact duration such as "2d 3h 4m".
func formatSeconds(total int) string {
	if total <= 0 {
		return "0"
	}
	days := total / 86400
	hours := (total % 86400) / 3600
	minutes := (total % 3600) / 60
	parts := make([]string, 0, 3)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	return strings.Join(parts, " ")
}

// compact renders a value on one line for table output.
func compact(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}
