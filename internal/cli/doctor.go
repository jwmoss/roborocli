package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/jwmoss/roborocli/internal/roborock"
)

// newDoctorCommand checks the login gateway, stored credentials, and, when
// possible, device reachability.
func newDoctorCommand(rc *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Verify configuration, credentials, and device reachability",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := rc.loadConfig()
			if err != nil {
				return err
			}
			rc.cfg = cfg
			report := map[string]any{}
			var problems []string

			// 1. Login gateway reachable?
			gateway := map[string]any{"ok": false}
			if cfg.Username != "" {
				cloud := rc.cloudClient(cfg)
				if base, err := cloud.BaseURL(ctx); err != nil {
					gateway["error"] = err.Error()
					problems = append(problems, "login gateway unreachable: "+err.Error())
				} else {
					country, countryCode := cloud.Country(ctx)
					gateway["ok"] = true
					gateway["base_url"] = base
					gateway["country"] = country
					gateway["country_code"] = countryCode
				}
			} else {
				gateway["error"] = "no account email configured"
				problems = append(problems, "no account email: run roborocli login --email you@example.com")
			}
			report["gateway"] = gateway

			// 2. Stored credentials valid?
			creds := map[string]any{"logged_in": false}
			if err := cfg.ValidateAuth(); err != nil {
				creds["error"] = err.Error()
				problems = append(problems, err.Error())
			} else {
				creds["logged_in"] = true
				creds["api_url"] = cfg.UserData.RRiot.R.A
				creds["mqtt_url"] = cfg.UserData.RRiot.R.M
				creds["region"] = cfg.UserData.Region
			}
			report["credentials"] = creds

			// 3. Device reachable?
			if creds["logged_in"] == true {
				device := map[string]any{}
				session, err := rc.openSession(ctx)
				switch {
				case err != nil:
					device["error"] = err.Error()
					problems = append(problems, "device lookup failed: "+err.Error())
				default:
					device["duid"] = session.Device.DUID
					device["name"] = session.Device.Name
					device["protocol"] = session.Device.PV
					device["firmware"] = session.Device.FV
					caller, err := rc.openCaller(ctx, session)
					if err != nil {
						device["error"] = err.Error()
						problems = append(problems, "transport failed: "+err.Error())
					} else {
						defer caller.Close()
						device["transport"] = caller.Transport()
						statusCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
						defer cancel()
						if _, err := caller.Call(statusCtx, roborock.CmdGetStatus, nil); err != nil {
							device["error"] = err.Error()
							problems = append(problems, "get_status failed: "+err.Error())
						} else {
							device["status_ok"] = true
						}
					}
				}
				report["device"] = device
			}

			report["ok"] = len(problems) == 0
			report["problems"] = problems
			if rc.out.IsJSON() || rc.out.IsPlain() {
				if err := rc.out.JSON(report); err != nil {
					return err
				}
			} else {
				rc.out.Table([]string{"CHECK", "RESULT"}, doctorRows(report))
			}
			if len(problems) > 0 {
				return fmt.Errorf("%d problem(s) found", len(problems))
			}
			rc.out.Success("all checks passed")
			return nil
		},
	}
}

// doctorRows flattens the report for table output.
func doctorRows(report map[string]any) [][]string {
	rows := make([][]string, 0, 8)
	for _, key := range []string{"gateway", "credentials", "device"} {
		section, ok := report[key].(map[string]any)
		if !ok {
			continue
		}
		status := "ok"
		if value, present := section["ok"]; present && value == false {
			status = "failed"
		}
		if _, present := section["error"]; present {
			status = "failed"
		}
		rows = append(rows, []string{key, status})
		if errText, present := section["error"]; present {
			rows = append(rows, []string{key + ".error", fmt.Sprintf("%v", errText)})
		}
	}
	return rows
}
