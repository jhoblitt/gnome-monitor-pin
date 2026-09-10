package cli

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"text/tabwriter"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

func printEntries(w io.Writer, entries []layout.Entry) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CONNECTOR\tVENDOR\tPRODUCT\tSERIAL\tPOSITION\tMODE\tSCALE\tTRANSFORM\tPRIMARY")
	for _, e := range entries {
		if e.Disabled {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\toff\t-\t-\t-\tno\n", e.Connector, e.Vendor, e.Product, e.Serial)
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d,%d\t%s\t%g\t%s\t%s\n",
			e.Connector, e.Vendor, e.Product, e.Serial, e.X, e.Y, e.Mode, e.Scale, e.Transform, yesNo(e.Primary))
	}
	return tw.Flush()
}

// printPlan lists each target cell beside where mutter shows that
// connector now, so a dry run reads as a diff.
func printPlan(w io.Writer, state layout.State, target layout.Target) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CONNECTOR\tCURRENT\tTARGET\tMODE\tSCALE\tTRANSFORM\tPRIMARY")
	for _, c := range target.Cells {
		fmt.Fprintf(tw, "%s\t%s\t%d,%d\t%s\t%g\t%s\t%s\n",
			c.Connector, currentPosition(state, c.Connector), c.X, c.Y, c.Mode, c.Scale, c.Transform, yesNo(c.Primary))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, id := range target.Missing {
		fmt.Fprintf(w, "missing: %s %s %s is not connected\n", id.Vendor, id.Product, id.Serial)
	}
	for _, c := range target.Unknown {
		fmt.Fprintf(w, "unknown: %s is not in the layout and is placed to the right\n", c)
	}
	for _, note := range target.Warnings {
		fmt.Fprintf(w, "note: %s\n", note)
	}
	return nil
}

func currentPosition(state layout.State, connector string) string {
	for _, l := range state.Logical {
		if slices.Contains(l.Connectors, connector) {
			return strconv.Itoa(l.X) + "," + strconv.Itoa(l.Y)
		}
	}
	return "off"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
