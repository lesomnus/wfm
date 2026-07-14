package cmd

import (
	"context"
	"fmt"

	"github.com/lesomnus/xli"
	"github.com/lesomnus/xli/arg"

	"github.com/lesomnus/wfm/internal/wifi"
)

func NewCmdInterface() *xli.Command {
	return &xli.Command{
		Name:    "interface",
		Aliases: []string{"if"},
		Brief:   "manage wireless interfaces",

		Commands: []*xli.Command{
			{
				Name:    "list",
				Aliases: []string{"ls"},
				Brief:   "list wireless interfaces",
				Handler: xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
					cc, err := dial(ctx, cmd)
					if err != nil {
						return err
					}
					defer cc.Close()

					resp, err := wifi.NewInterfaceServiceClient(cc).List(ctx, &wifi.InterfaceListRequest{})
					if err != nil {
						return err
					}
					for _, it := range resp.GetItems() {
						cmd.Printf("%-10s mac=%-17s powered=%-5t up=%-5t  %s\n",
							it.GetId(), it.GetMac(), it.GetPowered(), it.GetUp(), it.GetDesc())
					}
					return nil
				}),
			},
			{
				Name:  "power",
				Brief: "turn an interface on|off",
				Args: arg.Args{
					&arg.String{Name: "interface"},
					&arg.String{Name: "state"},
				},
				Handler: xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
					name, _ := arg.Get[string](cmd, "interface")
					st, _ := arg.Get[string](cmd, "state")
					on, err := parseOnOff(st)
					if err != nil {
						return err
					}

					cc, err := dial(ctx, cmd)
					if err != nil {
						return err
					}
					defer cc.Close()

					req := &wifi.InterfaceSetPowerRequest{}
					ref := &wifi.InterfaceRef{}
					ref.SetId(name)
					req.SetRef(ref)
					req.SetOn(on)

					it, err := wifi.NewInterfaceServiceClient(cc).SetPower(ctx, req)
					if err != nil {
						return err
					}
					cmd.Printf("%s powered=%t up=%t\n", it.GetId(), it.GetPowered(), it.GetUp())
					return nil
				}),
			},
		},

		Handler: xli.RequireSubcommand(),
	}
}

func parseOnOff(s string) (bool, error) {
	switch s {
	case "on", "up", "true", "1", "yes":
		return true, nil
	case "off", "down", "false", "0", "no":
		return false, nil
	default:
		return false, fmt.Errorf("state must be on|off (got %q)", s)
	}
}
