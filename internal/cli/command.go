package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jwmoss/roborocli/internal/roborock"
)

// newCommandCommand sends any device RPC method. It is the escape hatch for
// firmware features the CLI does not wrap.
func newCommandCommand(rc *runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "command <method> [params-json]",
		Short: "Send any device RPC method and print the result",
		Args:  usageArgs(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			method := args[0]
			readOnly := roborock.ReadOnlyCommands[method]
			if !readOnly && rc.g.dryRun && !force {
				return fmt.Errorf("dry-run: refusing to send mutating method %s (use --force to send anyway)", method)
			}
			var params any
			if len(args) == 2 && strings.TrimSpace(args[1]) != "" {
				if err := json.Unmarshal([]byte(args[1]), &params); err != nil {
					return fmt.Errorf("%w: params must be valid JSON: %v", errUsage, err)
				}
			}
			return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
				res, err := caller.Call(cmd.Context(), method, params)
				if err != nil {
					return err
				}
				if rc.out.IsJSON() {
					var pretty any
					if err := json.Unmarshal(res, &pretty); err == nil {
						return rc.out.JSON(map[string]any{
							"method":    method,
							"result":    pretty,
							"transport": caller.Transport(),
						})
					}
					return rc.out.JSON(map[string]any{"method": method, "raw": json.RawMessage(res)})
				}
				fmt.Fprintln(rc.stdout, compact(prettyOrRaw(res)))
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "send even under --dry-run")
	return cmd
}

// newRawCommand sends a Hawk-signed request to the authenticated rriot API.
func newRawCommand(rc *runtime) *cobra.Command {
	var (
		queryFlags []string
		formFlags  []string
		body       string
	)
	cmd := &cobra.Command{
		Use:   "raw <METHOD> <path>",
		Short: "Send a signed request to the Roborock cloud API",
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			method := strings.ToUpper(args[0])
			path := args[1]
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			query, err := parsePairs(queryFlags)
			if err != nil {
				return err
			}
			form, err := parsePairs(formFlags)
			if err != nil {
				return err
			}
			var jsonBody json.RawMessage
			if body != "" {
				if !json.Valid([]byte(body)) {
					return fmt.Errorf("%w: --body must be valid JSON", errUsage)
				}
				jsonBody = json.RawMessage(body)
			}
			cfg, err := rc.loadConfig()
			if err != nil {
				return err
			}
			if err := cfg.ValidateAuth(); err != nil {
				return err
			}
			if rc.g.dryRun && method != "GET" {
				return fmt.Errorf("dry-run: refusing %s request to the cloud", method)
			}
			cloud := rc.cloudClient(cfg)
			// Pass the response through as raw JSON so numbers keep their
			// exact value instead of rounding through float64.
			var out json.RawMessage
			if err := cloud.RawRequest(cmd.Context(), *cfg.UserData, method, path, query, form, jsonBody, &out); err != nil {
				return err
			}
			if rc.out.IsJSON() || rc.out.IsPlain() {
				var indented bytes.Buffer
				if err := json.Indent(&indented, out, "", "  "); err != nil {
					// Not JSON: emit it as a JSON string so output stays valid.
					return rc.out.JSON(string(out))
				}
				_, err := fmt.Fprintln(rc.stdout, indented.String())
				return err
			}
			fmt.Fprintln(rc.stdout, string(out))
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&queryFlags, "query", nil, "query parameter as key=value (repeatable)")
	cmd.Flags().StringArrayVar(&formFlags, "form", nil, "form field as key=value (repeatable)")
	cmd.Flags().StringVar(&body, "body", "", "JSON request body")
	return cmd
}

// parsePairs turns key=value flags into url.Values.
func parsePairs(values []string) (url.Values, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := url.Values{}
	for _, pair := range values {
		key, value, found := strings.Cut(pair, "=")
		if !found || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("%w: %q must be key=value", errUsage, pair)
		}
		out.Add(strings.TrimSpace(key), value)
	}
	return out, nil
}
