package cmd

import (
	"context"

	"github.com/lesomnus/xli"

	"github.com/lesomnus/wfm/internal/tui"
)

// runTUI dials the wifi server and starts the interactive terminal UI. It is the
// behavior of the root command when invoked with no subcommand.
func runTUI(ctx context.Context, cmd *xli.Command) error {
	cc, err := dial(ctx, cmd)
	if err != nil {
		return err
	}
	defer cc.Close()

	return tui.Run(ctx, cc)
}
