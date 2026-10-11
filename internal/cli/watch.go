package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jwmoss/roborocli/internal/roborock"
	"github.com/spf13/cobra"
)

func newWatchCommand(rc *runtime) *cobra.Command {
	var interval, duration time.Duration
	var count int
	cmd := &cobra.Command{Use: "watch", Short: "Stream device pushes and periodic live snapshots (JSON lines)", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error {
		if interval < time.Second || count < 0 || duration < 0 {
			return fmt.Errorf("%w: interval must be at least 1s; count and duration cannot be negative", errUsage)
		}
		return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
			encoder := json.NewEncoder(rc.stdout)
			emitted := 0
			emit := func(kind string, data any) error {
				emitted++
				return encoder.Encode(map[string]any{"time": time.Now().UTC(), "kind": kind, "transport": caller.Transport(), "data": data})
			}
			snapshot := func() error {
				raw, err := caller.Call(cmd.Context(), roborock.CmdGetStatus, nil)
				if err != nil {
					return err
				}
				return emit("snapshot", raw)
			}
			if err := snapshot(); err != nil {
				return err
			}
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			var deadline <-chan time.Time
			if duration > 0 {
				timer := time.NewTimer(duration)
				defer timer.Stop()
				deadline = timer.C
			}
			for count == 0 || emitted < count {
				select {
				case <-cmd.Context().Done():
					return nil
				case <-deadline:
					return nil
				case event := <-caller.Events():
					if err := emit(event.Kind, event); err != nil {
						return err
					}
					if event.Kind == "disconnected" {
						return fmt.Errorf("device stream disconnected: %s", event.Error)
					}
				case <-ticker.C:
					if err := snapshot(); err != nil {
						return err
					}
				}
			}
			return nil
		})
	}}
	cmd.Flags().DurationVar(&interval, "interval", 5*time.Second, "live snapshot interval")
	cmd.Flags().DurationVar(&duration, "duration", 0, "stop after this duration (0: until interrupted)")
	cmd.Flags().IntVar(&count, "count", 0, "stop after this many events (0: unlimited)")
	return cmd
}
