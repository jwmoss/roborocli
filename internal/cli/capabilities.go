package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jwmoss/roborocli/internal/roborock"
	"github.com/spf13/cobra"
)

func readCapabilities(ctx context.Context, session *deviceSession, caller Caller) (roborock.Capabilities, error) {
	family, err := roborock.Family(session.Device, session.Product)
	if err != nil {
		return roborock.Capabilities{}, err
	}
	if family != "v1" {
		if _, err := caller.Call(ctx, roborock.CmdGetStatus, nil); err != nil {
			return roborock.Capabilities{}, err
		}
		return roborock.FamilyCapabilities(family, session.Product.Model), nil
	}
	init, err := caller.Call(ctx, roborock.CmdGetInitStatus, nil)
	if err != nil {
		return roborock.Capabilities{}, err
	}
	status, err := caller.Call(ctx, roborock.CmdGetStatus, nil)
	if err != nil {
		return roborock.Capabilities{}, err
	}
	return roborock.DecodeCapabilities(session.Product.Model, session.User.Region, init, status)
}

func newCapabilitiesCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{Use: "capabilities", Short: "Read model features and supported named modes", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error {
		return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
			caps, err := readCapabilities(cmd.Context(), session, caller)
			if err != nil {
				return err
			}
			return rc.out.JSON(map[string]any{"capabilities": caps, "transport": caller.Transport()})
		})
	}}
}

func newModeCommand(rc *runtime) *cobra.Command {
	var fan, water, route string
	cmd := &cobra.Command{Use: "mode <vacuum|vac_and_mop|mop|custom|smart_mode>", Short: "Set a supported cleaning mode and verify the device settings", Args: usageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if rc.g.dryRun {
			return fmt.Errorf("dry-run: refusing to change cleaning mode")
		}
		return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
			caps, err := readCapabilities(cmd.Context(), session, caller)
			if err != nil {
				return err
			}
			params, err := caps.ModeParameters(args[0], fan, water, route)
			if err != nil {
				return err
			}
			method := "set_clean_motor_mode"
			var payload any = []map[string]int{params}
			if len(caps.Water) == 0 && (caps.Family == "" || caps.Family == "v1") {
				method = roborock.CmdSetCustomMode
				payload = []int{params["fan_power"]}
			}
			result, err := caller.Call(cmd.Context(), method, payload)
			if err != nil {
				return err
			}
			var actual map[string]json.RawMessage
			statusMethod := roborock.CmdGetStatus
			if caps.Family != "" && caps.Family != "v1" {
				statusMethod = "get_mode_status"
			}
			for attempt := 0; attempt < 5; attempt++ {
				status, err := caller.Call(cmd.Context(), statusMethod, nil)
				if err != nil {
					return fmt.Errorf("mode acknowledged but verification failed: %w", err)
				}
				actual, err = roborock.Object(status)
				if err != nil {
					return err
				}
				verified := true
				for key, value := range params {
					var got int
					if json.Unmarshal(actual[key], &got) != nil || got != value {
						verified = false
					}
				}
				if verified {
					return rc.out.JSON(map[string]any{"mode": args[0], "settings": params, "verified": true, "result": result, "transport": caller.Transport()})
				}
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-time.After(500 * time.Millisecond):
				}
			}
			return fmt.Errorf("mode acknowledged but settings did not match the requested values")
		})
	}}
	cmd.Flags().StringVar(&fan, "fan", "", "named suction level from capabilities")
	cmd.Flags().StringVar(&water, "water", "", "named water level from capabilities")
	cmd.Flags().StringVar(&route, "route", "", "named mop route from capabilities")
	return cmd
}
