package cli

import (
	"bytes"
	"fmt"
	"os"
	"strconv"

	"github.com/jwmoss/roborocli/internal/roborock"
	"github.com/spf13/cobra"
)

func (rc *runtime) mapClient(cmd *cobra.Command, session *deviceSession) (*roborock.MQTTClient, error) {
	client := roborock.NewMQTTClient(session.User.RRiot)
	client.Timeout = rc.g.timeout
	if err := client.ConfigureDevice(session.Device, session.Product); err != nil {
		return nil, err
	}
	if err := client.Connect(cmd.Context(), session.Device.DUID); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

func newMapsCommand(rc *runtime) *cobra.Command {
	root := &cobra.Command{Use: "maps", Short: "List, download, render, or switch device maps"}
	root.AddCommand(&cobra.Command{Use: "list", Short: "List saved floors", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error {
		session, err := rc.openSession(cmd.Context())
		if err != nil {
			return err
		}
		client, err := rc.mapClient(cmd, session)
		if err != nil {
			return err
		}
		defer client.Close()
		result, err := client.Call(cmd.Context(), session.Device.DUID, roborock.CmdGetMultiMapsList, nil)
		if err != nil {
			return err
		}
		return rc.out.JSON(map[string]any{"maps": result, "transport": "mqtt"})
	}})
	root.AddCommand(&cobra.Command{Use: "use <map_flag>", Short: "Switch the active floor and verify the selected map", Args: usageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if rc.g.dryRun {
			return fmt.Errorf("dry-run: refusing to switch maps")
		}
		flag, err := strconv.Atoi(args[0])
		if err != nil || flag < 0 || flag > 65535 {
			return fmt.Errorf("%w: invalid map flag", errUsage)
		}
		return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
			family, err := roborock.Family(session.Device, session.Product)
			if err != nil {
				return err
			}
			if family != "v1" {
				return fmt.Errorf("map selection currently requires a V1 device")
			}
			status, err := caller.Call(cmd.Context(), roborock.CmdGetStatus, nil)
			if err != nil {
				return err
			}
			object, err := firstObject(status)
			if err != nil {
				return err
			}
			if asInt(object["in_cleaning"]) != 0 || asInt(object["in_returning"]) != 0 {
				return fmt.Errorf("cannot switch maps during a task")
			}
			result, err := caller.Call(cmd.Context(), "load_multi_map", []int{flag})
			if err != nil {
				return err
			}
			status, err = caller.Call(cmd.Context(), roborock.CmdGetStatus, nil)
			if err != nil {
				return err
			}
			object, err = firstObject(status)
			if err != nil {
				return err
			}
			current := asInt(object["map_status"]) >> 2
			if current != flag {
				return fmt.Errorf("map switch acknowledged but active flag is %d", current)
			}
			return rc.out.JSON(map[string]any{"map_flag": flag, "verified": true, "result": result, "transport": caller.Transport()})
		})
	}})
	for _, name := range []string{"show", "download", "render"} {
		var record int64
		var output string
		cmd := &cobra.Command{Use: name, Short: "Read the current map (or a cleaning record map)", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error {
			if record < 0 {
				return fmt.Errorf("%w: record id must be positive", errUsage)
			}
			if name != "show" && output == "" {
				return fmt.Errorf("%w: --output is required", errUsage)
			}
			if output != "" && output != "-" && rc.g.dryRun {
				return fmt.Errorf("dry-run: refusing to write a map file")
			}
			session, err := rc.openSession(cmd.Context())
			if err != nil {
				return err
			}
			client, err := rc.mapClient(cmd, session)
			if err != nil {
				return err
			}
			defer client.Close()
			data, err := client.Map(cmd.Context(), session.Device.DUID, record)
			if err != nil {
				return err
			}
			var mapData *roborock.MapData
			if name != "download" {
				family, _ := roborock.Family(session.Device, session.Product)
				switch family {
				case "q7":
					mapData, err = roborock.ParseQ7Map(data)
				case "q10":
					mapData, err = roborock.ParseQ10Map(data)
				default:
					mapData, err = roborock.ParseMap(data)
				}
				if err != nil {
					return err
				}
			}
			if name == "show" {
				return rc.out.JSON(map[string]any{"map": mapData, "transport": "mqtt"})
			}
			if name == "render" {
				var buffer bytes.Buffer
				if err := mapData.PNG(&buffer); err != nil {
					return err
				}
				data = buffer.Bytes()
			}
			if output == "-" {
				_, err := rc.stdout.Write(data)
				return err
			}
			file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return err
			}
			_, writeErr := file.Write(data)
			closeErr := file.Close()
			if writeErr != nil {
				return writeErr
			}
			if closeErr != nil {
				return closeErr
			}
			return rc.out.JSON(map[string]any{"path": output, "bytes": len(data), "transport": "mqtt"})
		}}
		cmd.Flags().Int64Var(&record, "record", 0, "cleaning record id (0: current map)")
		if name != "show" {
			cmd.Flags().StringVar(&output, "output", "", "new output file, or - for binary stdout (existing files are refused)")
		}
		root.AddCommand(cmd)
	}
	return root
}
