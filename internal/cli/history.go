package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jwmoss/roborocli/internal/roborock"
	"github.com/spf13/cobra"
)

func addHistoryDetails(rc *runtime, parent *cobra.Command) {
	var limit int
	for _, name := range []string{"last", "records", "record <id>"} {
		command := &cobra.Command{Use: name, Short: "Read detailed cleaning results and completion evidence", Args: usageArgs(cobra.NoArgs)}
		if name == "record <id>" {
			command.Args = usageArgs(cobra.ExactArgs(1))
		}
		command.RunE = func(cmd *cobra.Command, args []string) error {
			if limit < 1 || limit > 100 {
				return fmt.Errorf("%w: --limit must be 1-100", errUsage)
			}
			return rc.withDevice(cmd.Context(), func(session *deviceSession, caller Caller) error {
				family, err := roborock.Family(session.Device, session.Product)
				if err != nil {
					return err
				}
				count := limit
				if cmd.Name() == "last" {
					count = 1
				}
				records, err := readHistoryRecords(cmd.Context(), caller, family, args, count)
				if err != nil {
					return err
				}
				if rc.out.IsJSON() {
					return rc.out.JSON(map[string]any{"records": records, "transport": caller.Transport()})
				}
				rows := make([][]string, 0, len(records))
				for _, r := range records {
					rows = append(rows, []string{fmt.Sprint(r.ID), r.Outcome, r.FinishReason, r.StartType, formatSeconds(int(r.Duration)), fmt.Sprintf("%.2f", r.Area), r.Error})
				}
				rc.out.Table([]string{"ID", "OUTCOME", "FINISH REASON", "START SOURCE", "DURATION", "AREA M2", "ERROR"}, rows)
				return nil
			})
		}
		if name == "records" {
			command.Flags().IntVar(&limit, "limit", 10, "maximum records to fetch (1-100)")
		}
		parent.AddCommand(command)
	}
}

func readHistoryRecords(ctx context.Context, caller Caller, family string, args []string, count int) ([]roborock.CleanRecord, error) {
	if family != "v1" {
		raw, err := caller.Call(ctx, roborock.CmdGetCleanSummary, nil)
		if err != nil {
			return nil, err
		}
		records, err := roborock.DecodeFamilyHistory(family, raw)
		if err != nil {
			return nil, err
		}
		if len(args) == 1 {
			for _, record := range records {
				if fmt.Sprint(record.ID) == args[0] || record.RecordKey == args[0] {
					return []roborock.CleanRecord{record}, nil
				}
			}
			return nil, fmt.Errorf("record %q is not in the device history", args[0])
		}
		return records[:min(len(records), count)], nil
	}
	var ids []int64
	if len(args) == 1 {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("%w: record id must be positive", errUsage)
		}
		ids = []int64{id}
	} else {
		raw, err := caller.Call(ctx, roborock.CmdGetCleanSummary, nil)
		if err != nil {
			return nil, err
		}
		ids, err = roborock.RecordIDs(raw)
		if err != nil {
			return nil, err
		}
		if len(ids) > count {
			ids = ids[:count]
		}
	}
	records := make([]roborock.CleanRecord, 0, len(ids))
	for _, id := range ids {
		raw, err := caller.Call(ctx, roborock.CmdGetCleanRecord, []int64{id})
		if err != nil {
			return nil, err
		}
		record, err := roborock.DecodeCleanRecord(id, raw)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}
