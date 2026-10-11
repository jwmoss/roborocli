package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jwmoss/roborocli/internal/roborock"
	"github.com/spf13/cobra"
)

func newRoutinesCommand(rc *runtime) *cobra.Command {
	root := &cobra.Command{Use: "routines", Short: "List or execute saved app routines"}
	list := &cobra.Command{Use: "list", Short: "List saved routines for the selected device", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error {
		session, err := rc.openSession(cmd.Context())
		if err != nil {
			return err
		}
		result, err := session.Cloud.Routines(cmd.Context(), session.User.RRiot, session.Device.DUID)
		if err != nil {
			return err
		}
		return rc.out.JSON(result)
	}}
	root.RunE = list.RunE
	root.Args = list.Args
	root.AddCommand(list, &cobra.Command{Use: "run <id|name>", Short: "Execute a saved routine for this device", Args: usageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if rc.g.dryRun {
			return fmt.Errorf("dry-run: refusing to execute a routine")
		}
		session, err := rc.openSession(cmd.Context())
		if err != nil {
			return err
		}
		routines, err := session.Cloud.Routines(cmd.Context(), session.User.RRiot, session.Device.DUID)
		if err != nil {
			return err
		}
		var matches []roborock.Routine
		for _, r := range routines {
			if strconv.FormatInt(r.ID, 10) == args[0] || strings.EqualFold(r.Name, args[0]) {
				matches = append(matches, r)
			}
		}
		if len(matches) != 1 {
			return fmt.Errorf("routine %q matched %d routines on this device", args[0], len(matches))
		}
		r := matches[0]
		if r.Enabled != nil && !*r.Enabled {
			return fmt.Errorf("routine %q is disabled", r.Name)
		}
		if err := session.Cloud.ExecuteRoutine(cmd.Context(), session.User.RRiot, r.ID); err != nil {
			return err
		}
		return rc.out.JSON(map[string]any{"id": r.ID, "name": r.Name, "submitted": true, "transport": "cloud"})
	}})
	return root
}
