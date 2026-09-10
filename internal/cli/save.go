package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

func newSaveCmd(v *viper.Viper, connect Connect) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "save",
		Short: "Write the current layout to the layout file",
		Long: `Write the current layout to the layout file.

Arrange the monitors in GNOME Settings first and click Keep Changes; save
records that arrangement keyed on each monitor's vendor, product, and
serial, so it survives the connector renames a DisplayPort MST hotplug
causes. A monitor switched off in Settings is recorded as disabled. Run
"show" first if unsure what mutter shows now.`,
		Args: cobra.NoArgs,
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
			l := layout.Layout{Monitors: make([]layout.Placement, 0, len(entries))}
			for _, e := range entries {
				l.Monitors = append(l.Monitors, e.Placement)
			}
			warnDuplicates(ctx, entries)
			path := v.GetString("layout")
			if err := writeLayout(path, l, v.GetBool("force")); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "saved %d monitors to %s\n", len(l.Monitors), path)
			if err := printEntries(out, entries); err != nil {
				return err
			}
			fmt.Fprintln(out, "If this is not the arrangement you want, arrange it in Settings, click Keep Changes, then run save --force.")
			return nil
		},
	}
	cmd.Flags().Bool("force", false, "overwrite an existing layout file (GNOME_MONITOR_PIN_FORCE)")
	return cmd
}

// warnDuplicates logs every identity two captured monitors share, since
// fix can only tell such monitors apart by order.
func warnDuplicates(ctx context.Context, entries []layout.Entry) {
	seen := make(map[layout.ID]string, len(entries))
	for _, e := range entries {
		if other, dup := seen[e.ID]; dup {
			slog.WarnContext(ctx, "monitors share identity and will be matched by order",
				slog.String("serial", e.Serial), slog.String("connector", e.Connector), slog.String("other", other))
			continue
		}
		seen[e.ID] = e.Connector
	}
}
