package cli

import (
	"encoding/json"
	"fmt"

	"github.com/jwmoss/roborocli/internal/roborock"
	"github.com/spf13/cobra"
)

func newDockCommand(rc *runtime) *cobra.Command {
	root := &cobra.Command{Use: "dock", Short: "Inspect and control mop washing, drying, and dust collection"}
	root.AddCommand(&cobra.Command{Use: "status", Short: "Read live dock state and settings", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error {
		return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
			status, err := caller.Call(cmd.Context(), roborock.CmdGetStatus, nil)
			if err != nil {
				return err
			}
			values := map[string]any{"status": status, "transport": caller.Transport()}
			errors := map[string]string{}
			for _, method := range []string{"get_dock_info", "get_wash_towel_mode", "get_dust_collection_mode", "app_get_dryer_setting", "get_wash_towel_params", "get_smart_wash_params"} {
				value, err := caller.Call(cmd.Context(), method, nil)
				if err != nil {
					errors[method] = err.Error()
				} else {
					values[method] = value
				}
			}
			if len(errors) > 0 {
				values["unavailable"] = errors
			}
			return rc.out.JSON(values)
		})
	}})
	for _, action := range []struct {
		name, method, feature string
		params                any
	}{
		{"wash-start", "app_start_wash", "is_washable", nil}, {"wash-stop", "app_stop_wash", "is_washable", nil},
		{"dry-start", "app_set_dryer_status", "is_dryable", map[string]int{"status": 1}}, {"dry-stop", "app_set_dryer_status", "is_dryable", map[string]int{"status": 0}},
		{"empty-start", "app_start_collect_dust", "is_collectable", nil}, {"empty-stop", "app_stop_collect_dust", "is_collectable", nil},
	} {
		root.AddCommand(&cobra.Command{Use: action.name, Short: "Send the dock action", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error {
			if rc.g.dryRun {
				return fmt.Errorf("dry-run: refusing dock action")
			}
			return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
				caps, err := readCapabilities(cmd.Context(), session, caller)
				if err != nil {
					return err
				}
				if !caps.Dock[action.feature] {
					return fmt.Errorf("dock does not support %s", action.name)
				}
				return rc.sendMutating(cmd, caller, action.method, action.params, "dock command acknowledged; use dock status to verify")
			})
		}})
	}
	for _, setting := range []struct {
		name, get, set, key, feature string
		choices                      map[string]int
	}{
		{"auto-dry", "app_get_dryer_setting", "app_set_dryer_setting", "status", "is_dryable", map[string]int{"on": 1, "off": 0}},
		{"wash-mode", "get_wash_towel_mode", "set_wash_towel_mode", "wash_mode", "is_washable", map[string]int{"light": 0, "balanced": 1, "deep": 2, "super_deep": 8, "smart": 10}},
		{"empty-mode", "get_dust_collection_mode", "set_dust_collection_mode", "mode", "is_collectable", map[string]int{"smart": 0, "light": 1, "balanced": 2, "max": 3}},
	} {
		root.AddCommand(&cobra.Command{Use: setting.name + " [value]", Short: "Read or set " + setting.name, Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return rc.showSetting(cmd, setting.get)
			}
			value, ok := setting.choices[args[0]]
			if !ok {
				return fmt.Errorf("%w: invalid %s; choices: %v", errUsage, setting.name, setting.choices)
			}
			if rc.g.dryRun {
				return fmt.Errorf("dry-run: refusing to change dock settings")
			}
			return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
				caps, err := readCapabilities(cmd.Context(), session, caller)
				if err != nil {
					return err
				}
				if !caps.Dock[setting.feature] {
					return fmt.Errorf("dock does not support %s", setting.name)
				}
				if setting.name == "wash-mode" {
					smart := caps.Features["is_dirty_replenish_clean_supported"]
					if value == 10 && !smart || value == 8 && (smart || !caps.Features["is_super_deep_wash_supported"]) {
						return fmt.Errorf("dock does not support wash mode %s", args[0])
					}
				}
				if _, err := caller.Call(cmd.Context(), setting.set, map[string]int{setting.key: value}); err != nil {
					return err
				}
				raw, err := caller.Call(cmd.Context(), setting.get, nil)
				if err != nil {
					return fmt.Errorf("setting acknowledged but verification failed: %w", err)
				}
				object, err := roborock.Object(raw)
				if err != nil {
					return err
				}
				var actual int
				if json.Unmarshal(object[setting.key], &actual) != nil || actual != value {
					return fmt.Errorf("setting acknowledged but %s did not match", setting.key)
				}
				return rc.out.JSON(map[string]any{"setting": setting.name, "value": value, "verified": true, "transport": caller.Transport()})
			})
		}})
	}
	return root
}
