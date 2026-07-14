package cmd

import (
	"context"
	"fmt"

	"github.com/lesomnus/xli"
	"github.com/lesomnus/xli/arg"

	wifi "github.com/lesomnus/wfm/internal/wifi"
)

func NewCmdScan() *xli.Command {
	return &xli.Command{
		Name:  "scan",
		Brief: "scan access points visible to an interface",
		Args: arg.Args{
			&arg.String{Name: "interface"},
		},
		Handler: xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
			ifname, _ := arg.Get[string](cmd, "interface")
			if ifname == "" {
				return fmt.Errorf("interface is required")
			}

			cc, err := dial(cmd)
			if err != nil {
				return err
			}
			defer cc.Close()

			req := &wifi.AccessPointScanRequest{}
			ref := &wifi.InterfaceRef{}
			ref.SetId(ifname)
			req.SetInterface(ref)

			resp, err := wifi.NewAccessPointServiceClient(cc).Scan(ctx, req)
			if err != nil {
				return err
			}
			for _, ap := range resp.GetItems() {
				cmd.Printf("%-32s %-17s sig=%3d freq=%-5d %s\n",
					ap.GetSsid(), ap.GetBssid(), ap.GetSignal(), ap.GetFrequency(), kmStr(ap.GetKeyManagement()))
			}
			return nil
		}),
	}
}
