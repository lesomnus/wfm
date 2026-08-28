package cmd

import (
	"context"

	"github.com/lesomnus/otx"
	"github.com/lesomnus/xli"
	"github.com/lesomnus/xli/flg"
	"github.com/lesomnus/xli/frm"
)

func NewCmdRoot() *xli.Command {
	return &xli.Command{
		Name: "wfm",

		Flags: flg.Flags{
			&flg.String{Name: "config", Brief: "path to config file"},
			&flg.String{Name: "server", Brief: "gRPC server address (default: unix socket, else in-process)"},
			&flg.String{Name: "backend", Brief: "wifi backend for an in-process server: nmcli|nmdbus|iwd|ubus (default: autodetect)"},
		},

		Commands: []*xli.Command{
			NewCmdVersion(),
			NewCmdConfig(),

			NewCmdInterface(),
			NewCmdScan(),
			NewCmdProfile(),
			NewCmdConnect(),
			NewCmdConnection(),

			NewCmdServe(),
		},

		Handler: xli.Chain(
			xli.OnRunPass(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
				if frm.HasSeq(frm.From(ctx).Next(), "version") {
					return next(ctx)
				}

				ctx, _, err := UseConfigInit(ctx, cmd)
				if err != nil {
					return err
				}

				o := otx.From(ctx)
				defer o.Shutdown(ctx)

				return next(ctx)
			}),
			// No subcommand: start the interactive TUI. OnRun fires only when
			// the root is the terminal command; with a subcommand it passes
			// through to dispatch. It runs after config init above so the TUI's
			// RPCs share the same otx as the CLI.
			xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
				return runTUI(ctx, cmd)
			}),
		),
	}
}
