package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jwmoss/roborocli/internal/config"
	"github.com/jwmoss/roborocli/internal/roborock"
)

func newLoginCommand(rc *runtime) *cobra.Command {
	var (
		email    string
		code     string
		sendOnly bool
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in with an email code and save cloud credentials",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := rc.loadConfig()
			if err != nil {
				return err
			}
			if email != "" {
				cfg.Username = email
			}
			if cfg.Username == "" {
				return fmt.Errorf("account email is required: roborocli login --email you@example.com")
			}
			if cfg.ClientID == "" {
				cfg.ClientID = roborock.ClientIDFromSeed(installSeed())
			}
			if rc.g.dryRun {
				return fmt.Errorf("dry-run: refusing to write credentials")
			}

			cloud := roborock.NewCloudClient(cfg.Username, cfg.ClientID, nil)
			if cfg.BaseURL != "" {
				cloud.SetBaseURL(cfg.BaseURL)
			}
			if rc.g.traceHTTP {
				cloud.Trace = traceFn(rc)
			}
			base, err := cloud.BaseURL(cmd.Context())
			if err != nil {
				return err
			}
			country, countryCode := cloud.Country(cmd.Context())
			if os.Getenv("ROBOROCLI_DEBUG_LOGIN") == "1" {
				fmt.Fprintf(os.Stderr, "debug: base=%s country=%q countryCode=%q\n", base, country, countryCode)
			}

			if sendOnly || code == "" {
				if err := cloud.SendEmailCode(cmd.Context()); err != nil {
					return err
				}
				if rc.out.IsJSON() {
					return rc.out.JSON(map[string]string{
						"status":   "code_sent",
						"email":    cfg.Username,
						"base_url": base,
						"country":  country,
					})
				}
				rc.out.Success("login code sent")
				rc.out.Printf("email: %s\n", cfg.Username)
				if country != "" {
					rc.out.Printf("country: %s (%s)\n", country, countryCode)
				}
				if sendOnly {
					return nil
				}
				entered, err := rc.readCode(cmd)
				if err != nil {
					return err
				}
				code = entered
			}

			user, err := cloud.Login(cmd.Context(), code)
			if err != nil {
				return err
			}
			next := *cfg
			next.UserData = user
			next.BaseURL = base
			if err := config.Save(rc.configPath(), next, true); err != nil {
				return err
			}
			if rc.out.IsJSON() {
				return rc.out.JSON(map[string]any{
					"status":  "logged_in",
					"email":   cfg.Username,
					"region":  user.Region,
					"country": user.Country,
					"api_url": user.RRiot.R.A,
				})
			}
			rc.out.Success("logged in")
			rc.out.Printf("email: %s\n", cfg.Username)
			rc.out.Printf("api: %s\n", user.RRiot.R.A)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "Roborock account email")
	cmd.Flags().StringVar(&code, "code", "", "login code from the email (skips the prompt)")
	cmd.Flags().BoolVar(&sendOnly, "send-code-only", false, "send the code and exit without prompting")
	return cmd
}

func newLogoutCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Clear stored cloud credentials",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := rc.loadConfig()
			if err != nil {
				return err
			}
			if rc.g.dryRun {
				return fmt.Errorf("dry-run: refusing to clear credentials")
			}
			next := *cfg
			next.UserData = nil
			if err := config.Save(rc.configPath(), next, true); err != nil {
				return err
			}
			rc.out.Success("credentials cleared")
			return nil
		},
	}
}

func newWhoamiCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the stored account and region",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := rc.loadConfig()
			if err != nil {
				return err
			}
			payload := map[string]any{
				"email":     cfg.Username,
				"logged_in": cfg.UserData != nil && cfg.UserData.RRiot.U != "",
			}
			if cfg.UserData != nil {
				payload["region"] = cfg.UserData.Region
				payload["country"] = cfg.UserData.Country
				payload["api_url"] = cfg.UserData.RRiot.R.A
				payload["mqtt_url"] = cfg.UserData.RRiot.R.M
				payload["user_id"] = cfg.UserData.RRiot.U
			}
			if rc.out.IsJSON() {
				return rc.out.JSON(payload)
			}
			rows := [][]string{
				{"email", cfg.Username},
				{"logged_in", fmt.Sprintf("%v", payload["logged_in"])},
			}
			if cfg.UserData != nil {
				rows = append(rows,
					[]string{"region", cfg.UserData.Region},
					[]string{"country", cfg.UserData.Country},
					[]string{"api_url", cfg.UserData.RRiot.R.A},
					[]string{"mqtt_url", cfg.UserData.RRiot.R.M},
					[]string{"user_id", cfg.UserData.RRiot.U},
				)
			}
			rc.out.Table([]string{"KEY", "VALUE"}, rows)
			return nil
		},
	}
}

// readCode prompts for the emailed login code.
func (rc *runtime) readCode(cmd *cobra.Command) (string, error) {
	if rc.g.noInput {
		return "", fmt.Errorf("interactive prompt disabled; pass --code")
	}
	file, ok := rc.stdin.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		// Fall back to a plain stdin read for pipes and tests.
		reader := bufio.NewReader(rc.stdin)
		_, _ = fmt.Fprint(rc.stderr, "Roborock login code: ")
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("read login code: %w", err)
		}
		code := strings.TrimSpace(line)
		if code == "" {
			return "", fmt.Errorf("login code is empty")
		}
		return code, nil
	}
	_, _ = fmt.Fprint(rc.stderr, "Roborock login code: ")
	data, err := term.ReadPassword(int(file.Fd()))
	_, _ = fmt.Fprintln(rc.stderr)
	if err != nil {
		return "", fmt.Errorf("read login code: %w", err)
	}
	code := strings.TrimSpace(string(data))
	if code == "" {
		return "", fmt.Errorf("login code is empty")
	}
	return code, nil
}

// installSeed derives a stable per-machine seed for the install identifier.
func installSeed() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "roborocli"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return host
	}
	return host + "|" + home
}

// traceFn returns a cloud request tracer.
func traceFn(rc *runtime) func(string, string, int, time.Duration) {
	return func(method, path string, status int, d time.Duration) {
		rc.out.Errorf("[cloud] %s %s -> %d (%s)", method, path, status, d)
	}
}
