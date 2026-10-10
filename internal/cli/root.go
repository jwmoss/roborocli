package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"time"

	"github.com/spf13/cobra"

	"github.com/jwmoss/roborocli/internal/config"
	"github.com/jwmoss/roborocli/internal/output"
)

const (
	exitOK    = 0
	exitErr   = 1
	exitUsage = 2
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

type globals struct {
	configPath  string
	baseURL     string
	device      string
	host        string
	mqtt        bool
	asJSON      bool
	plain       bool
	quiet       bool
	noColor     bool
	showVersion bool
	timeout     time.Duration
	traceHTTP   bool
	dryRun      bool
	noInput     bool
}

type runtime struct {
	ctx    context.Context
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	g      *globals
	cfg    *config.Config
	out    *output.Formatter
}

func Execute(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	rc := &runtime{
		ctx:    ctx,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
		g:      &globals{timeout: 15 * time.Second},
	}

	cmd := newRootCommand(rc)
	cmd.SetArgs(args)
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	if err := cmd.ExecuteContext(ctx); err != nil {
		if !errors.Is(err, errSilent) {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		}
		_, _, findErr := cmd.Find(args)
		if errors.Is(err, errUsage) || findErr != nil {
			return exitUsage
		}
		return exitErr
	}
	return exitOK
}

func newRootCommand(rc *runtime) *cobra.Command {
	root := &cobra.Command{
		Use:           "roborocli",
		Short:         "Command-line client for Roborock robot vacuums",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if rc.g.showVersion {
				return rc.writeVersion()
			}
			_ = cmd.Help()
			return errUsage
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if rc.g.asJSON && rc.g.plain {
				return fmt.Errorf("%w: choose only one of --json or --plain", errUsage)
			}
			if rc.g.timeout <= 0 {
				return fmt.Errorf("%w: --timeout must be positive", errUsage)
			}
			rc.out = output.New(rc.stdout, rc.stderr, rc.g.asJSON, rc.g.plain, rc.g.quiet, rc.g.noColor)
			return nil
		},
	}

	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return fmt.Errorf("%w: %v", errUsage, err)
	})
	root.Flags().BoolVar(&rc.g.showVersion, "version", false, "print version and exit")
	flags := root.PersistentFlags()
	flags.StringVar(&rc.g.configPath, "config", "", "config file path")
	flags.StringVar(&rc.g.baseURL, "base-url", "", "login gateway base URL override")
	flags.StringVar(&rc.g.device, "device", "", "device duid or name")
	flags.StringVar(&rc.g.device, "duid", "", "device duid or name (alias)")
	flags.StringVar(&rc.g.host, "host", "", "vacuum local IP for direct TCP control")
	flags.BoolVar(&rc.g.mqtt, "mqtt", false, "force control through the cloud MQTT broker")
	flags.BoolVar(&rc.g.asJSON, "json", false, "emit JSON to stdout")
	flags.BoolVar(&rc.g.plain, "plain", false, "emit stable plain text where available")
	flags.BoolVarP(&rc.g.quiet, "quiet", "q", false, "suppress non-essential output")
	flags.BoolVar(&rc.g.noColor, "no-color", false, "disable color")
	flags.DurationVar(&rc.g.timeout, "timeout", 15*time.Second, "command timeout")
	flags.BoolVar(&rc.g.traceHTTP, "trace-http", false, "log cloud requests to stderr")
	flags.BoolVar(&rc.g.dryRun, "dry-run", false, "refuse mutating commands and local config writes")
	flags.BoolVar(&rc.g.noInput, "no-input", false, "disable interactive prompts")

	root.AddCommand(newVersionCommand(rc))
	root.AddCommand(newConfigCommand(rc))
	root.AddCommand(newDoctorCommand(rc))
	root.AddCommand(newLoginCommand(rc))
	root.AddCommand(newLogoutCommand(rc))
	root.AddCommand(newWhoamiCommand(rc))
	root.AddCommand(newDevicesCommand(rc))
	root.AddCommand(newDiscoverCommand(rc))
	root.AddCommand(newStatusCommand(rc))
	root.AddCommand(newInfoCommand(rc))
	root.AddCommand(newSettingsCommand(rc))
	root.AddCommand(newControlCommand(rc))
	root.AddCommand(newCommandCommand(rc))
	root.AddCommand(newRawCommand(rc))
	root.AddCommand(newCompletionCommand(root))

	return root
}

func newVersionCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return rc.writeVersion()
		},
	}
}

func currentVersion() (string, string, string) {
	v, c, d := version, commit, date
	if info, ok := debug.ReadBuildInfo(); ok {
		if v == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if c == "unknown" {
					c = setting.Value
				}
			case "vcs.time":
				if d == "unknown" {
					d = setting.Value
				}
			}
		}
	}
	return v, c, d
}

func (rc *runtime) writeVersion() error {
	v, c, d := currentVersion()
	payload := map[string]string{
		"version": v,
		"commit":  c,
		"date":    d,
	}
	if rc.out.IsJSON() {
		return rc.out.JSON(payload)
	}
	if rc.out.IsPlain() {
		rc.out.Printf("%s\n", v)
		return nil
	}
	rc.out.Printf("roborocli version %s\n", v)
	rc.out.Printf("commit: %s\n", c)
	rc.out.Printf("built:  %s\n", d)
	return nil
}

func newCompletionCommand(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return root.GenPowerShellCompletion(cmd.OutOrStdout())
			default:
				return fmt.Errorf("%w: unsupported shell %q", errUsage, args[0])
			}
		},
	}
	return cmd
}

var (
	errUsage  = errors.New("invalid usage")
	errSilent = errors.New("silent")
)

func usageArgs(fn cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := fn(cmd, args); err != nil {
			return fmt.Errorf("%w: %v", errUsage, err)
		}
		return nil
	}
}

func init() {
	cobra.EnableCommandSorting = false
}
