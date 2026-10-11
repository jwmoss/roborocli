package cli

import (
	"encoding/json"
	"fmt"

	"github.com/jwmoss/roborocli/internal/roborock"
	"github.com/spf13/cobra"
)

func newDatapointsCommand(rc *runtime) *cobra.Command {
	root := &cobra.Command{Use: "datapoints", Short: "Inspect or control A01 and Q10 named data points"}
	root.AddCommand(&cobra.Command{Use: "list", Short: "List protocol data point names and codes", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error {
		session, err := rc.openSession(cmd.Context())
		if err != nil {
			return err
		}
		family, err := roborock.Family(session.Device, session.Product)
		if err != nil {
			return err
		}
		return rc.out.JSON(map[string]any{"family": family, "datapoints": roborock.Datapoints(family)})
	}})
	root.AddCommand(&cobra.Command{Use: "get <name|code>...", Short: "Query live data point values", Args: usageArgs(cobra.MinimumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
			family, err := roborock.Family(session.Device, session.Product)
			if err != nil {
				return err
			}
			if family == "v1" || family == "q7" {
				return fmt.Errorf("use command for %s property reads", family)
			}
			result, err := caller.Call(cmd.Context(), "datapoints.get", args)
			if err != nil {
				return err
			}
			return rc.out.JSON(map[string]any{"family": family, "datapoints": result, "transport": caller.Transport()})
		})
	}})
	root.AddCommand(&cobra.Command{Use: "set <name|code> <json-value>", Short: "Publish a data point write (use get to verify)", Args: usageArgs(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		if rc.g.dryRun {
			return fmt.Errorf("dry-run: refusing data point write")
		}
		if !json.Valid([]byte(args[1])) {
			return fmt.Errorf("%w: value must be valid JSON", errUsage)
		}
		return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
			family, err := roborock.Family(session.Device, session.Product)
			if err != nil {
				return err
			}
			if family == "v1" || family == "q7" {
				return fmt.Errorf("use command for %s writes", family)
			}
			code, err := roborock.DatapointCode(family, args[0])
			if err != nil {
				return err
			}
			result, err := caller.Call(cmd.Context(), code, json.RawMessage(args[1]))
			if err != nil {
				return err
			}
			return rc.out.JSON(map[string]any{"family": family, "datapoint": code, "result": result, "transport": caller.Transport()})
		})
	}})
	return root
}
