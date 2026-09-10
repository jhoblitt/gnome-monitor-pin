package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
	"github.com/jhoblitt/gnome-monitor-pin/internal/pin"
)

func newFixCmd(v *viper.Viper, connect Connect) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fix",
		Short: "Apply the pinned layout once",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			path := v.GetString("layout")
			if _, err := readLayout(path); err != nil {
				return err
			}
			d, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closeDisplay(ctx, d)
			dryRun := v.GetBool("dry-run")
			p := &pin.Pinner{Display: d, Load: func() (layout.Layout, error) { return readLayout(path) }}
			res, err := p.Fix(ctx, dryRun)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch {
			case dryRun:
				if !res.Changed {
					fmt.Fprintln(out, "layout already pinned; would apply:")
				} else {
					fmt.Fprintln(out, "would apply:")
				}
				return printPlan(out, res.State, res.Target)
			case !res.Changed:
				fmt.Fprintln(out, "layout already pinned")
			default:
				fmt.Fprintf(out, "applied layout to %d monitors\n", len(res.Target.Cells))
			}
			return nil
		},
	}
	cmd.Flags().Bool("dry-run", false, "verify the derived layout with mutter and print it without applying (GNOME_MONITOR_PIN_DRY_RUN)")
	return cmd
}
