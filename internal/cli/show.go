package cli

import (
	"github.com/spf13/cobra"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

func newShowCmd(connect Connect) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the connected monitors and where mutter shows them",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			d, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closeDisplay(ctx, d)
			state, err := d.CurrentState(ctx)
			if err != nil {
				return err
			}
			entries, err := layout.Snapshot(state)
			if err != nil {
				return err
			}
			return printEntries(cmd.OutOrStdout(), entries)
		},
	}
}
