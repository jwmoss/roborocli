package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jwmoss/roborocli/internal/config"
	"github.com/jwmoss/roborocli/internal/roborock"
)

func newConfigCommand(rc *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and initialize configuration",
	}
	cmd.AddCommand(newConfigShowCommand(rc))
	cmd.AddCommand(newConfigInitCommand(rc))
	return cmd
}

func newConfigShowCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show effective configuration with secrets redacted",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := rc.loadConfig()
			if err != nil {
				return err
			}
			path := rc.configPath()
			redacted := cfg.Redacted()
			redacted["path"] = path
			if rc.out.IsJSON() {
				return rc.out.JSON(redacted)
			}
			rows := [][]string{
				{"base_url", cfg.BaseURL},
				{"username", cfg.Username},
				{"client_id", cfg.ClientID},
				{"logged_in", fmt.Sprintf("%v", redacted["logged_in"])},
				{"default_duid", cfg.DefaultDUID},
				{"default_host", cfg.DefaultHost},
				{"path", path},
			}
			rc.out.Table([]string{"KEY", "VALUE"}, rows)
			return nil
		},
	}
}

func newConfigInitCommand(rc *runtime) *cobra.Command {
	var (
		email       string
		defaultDUID string
		defaultHost string
		force       bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a config file",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if rc.g.dryRun {
				return fmt.Errorf("dry-run: refusing to write config")
			}
			path := rc.configPath()
			if !force && fileExists(path) {
				return fmt.Errorf("config already exists at %s; use --force to overwrite", path)
			}
			cfg := config.Default()
			if rc.g.baseURL != "" {
				cfg.BaseURL = rc.g.baseURL
			}
			cfg.Username = email
			cfg.DefaultDUID = defaultDUID
			cfg.DefaultHost = defaultHost
			cfg.ClientID = roborock.ClientIDFromSeed(installSeed())
			if err := config.Save(path, cfg, force); err != nil {
				return err
			}
			if rc.out.IsJSON() {
				return rc.out.JSON(map[string]string{"path": path, "status": "written"})
			}
			rc.out.Success("config written")
			rc.out.Printf("%s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "Roborock account email")
	cmd.Flags().StringVar(&defaultDUID, "default-duid", "", "device duid used when --device is omitted")
	cmd.Flags().StringVar(&defaultHost, "default-host", "", "vacuum IP used when --host is omitted")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config file")
	return cmd
}
