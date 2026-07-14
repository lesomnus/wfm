package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/lesomnus/xli"
	"github.com/lesomnus/xli/arg"
	"github.com/lesomnus/xli/flg"

	"github.com/lesomnus/wfm/internal/wifi"
)

func NewCmdProfile() *xli.Command {
	return &xli.Command{
		Name:    "profile",
		Aliases: []string{"prof"},
		Brief:   "manage saved connection profiles",

		Commands: []*xli.Command{
			{
				Name:    "list",
				Aliases: []string{"ls"},
				Brief:   "list saved profiles",
				Handler: xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
					cc, err := dial(cmd)
					if err != nil {
						return err
					}
					defer cc.Close()

					resp, err := wifi.NewProfileServiceClient(cc).List(ctx, &wifi.ProfileListRequest{})
					if err != nil {
						return err
					}
					for _, p := range resp.GetItems() {
						cmd.Printf("%s  ssid=%-24s name=%-16s sec=%s autoconnect=%t\n",
							uuidStr(p.GetId()), p.GetSsid(), p.GetName(), secStr(p.GetSecurity()), p.GetAutoconnect())
					}
					return nil
				}),
			},
			newCmdProfileAdd(),
			{
				Name:  "rm",
				Brief: "delete a saved profile",
				Args: arg.Args{
					&arg.String{Name: "uuid"},
				},
				Handler: xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
					id, _ := arg.Get[string](cmd, "uuid")
					b, err := uuidBytes(id)
					if err != nil {
						return fmt.Errorf("invalid uuid: %w", err)
					}
					cc, err := dial(cmd)
					if err != nil {
						return err
					}
					defer cc.Close()

					ref := &wifi.ProfileRef{}
					ref.SetId(b)
					if _, err := wifi.NewProfileServiceClient(cc).Erase(ctx, ref); err != nil {
						return err
					}
					cmd.Printf("deleted %s\n", id)
					return nil
				}),
			},
		},

		Handler: xli.RequireSubcommand(),
	}
}

func newCmdProfileAdd() *xli.Command {
	return &xli.Command{
		Name:  "add",
		Brief: "create a saved profile",
		Flags: flg.Flags{
			&flg.String{Name: "ssid", Brief: "network name (required)"},
			&flg.String{Name: "psk", Brief: "passphrase (omit for open network)"},
			&flg.String{Name: "name", Brief: "profile name (defaults to ssid)"},
			&flg.Switch{Name: "hidden", Brief: "network does not broadcast its ssid"},
			&flg.Switch{Name: "no-autoconnect", Brief: "do not auto-connect when in range"},
			&flg.String{Name: "ip4", Brief: "ipv4 method: auto|manual|disabled"},
			&flg.String{Name: "ip4-addr", Brief: "manual ipv4 addresses, comma-separated CIDR"},
			&flg.String{Name: "ip4-gw", Brief: "manual ipv4 gateway"},
			&flg.String{Name: "ip4-dns", Brief: "manual ipv4 dns, comma-separated"},
		},
		Handler: xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
			ssid, _ := flg.Get[string](cmd, "ssid")
			if ssid == "" {
				return fmt.Errorf("--ssid is required")
			}
			psk, _ := flg.Get[string](cmd, "psk")
			name, _ := flg.Get[string](cmd, "name")
			hidden, _ := flg.Get[bool](cmd, "hidden")
			no_ac, _ := flg.Get[bool](cmd, "no-autoconnect")

			req := &wifi.ProfileAddRequest{}
			req.SetSsid(ssid)
			if name != "" {
				req.SetName(name)
			}
			req.SetHidden(hidden)
			req.SetAutoconnect(!no_ac)

			sec := &wifi.Security{}
			if psk != "" {
				p := &wifi.Security_Psk{}
				p.SetPassphrase(psk)
				sec.SetPsk(p)
			} else {
				sec.SetOpen(&wifi.Security_Open{})
			}
			req.SetSecurity(sec)

			if m, _ := flg.Get[string](cmd, "ip4"); m != "" {
				addr, _ := flg.Get[string](cmd, "ip4-addr")
				gw, _ := flg.Get[string](cmd, "ip4-gw")
				dns, _ := flg.Get[string](cmd, "ip4-dns")
				cfg, err := buildIpConfig(m, addr, gw, dns)
				if err != nil {
					return err
				}
				req.SetIpv4(cfg)
			}

			cc, err := dial(cmd)
			if err != nil {
				return err
			}
			defer cc.Close()

			p, err := wifi.NewProfileServiceClient(cc).Add(ctx, req)
			if err != nil {
				return err
			}
			cmd.Printf("created %s ssid=%s\n", uuidStr(p.GetId()), p.GetSsid())
			return nil
		}),
	}
}

func buildIpConfig(method, addr, gw, dns string) (*wifi.IpConfig, error) {
	cfg := &wifi.IpConfig{}
	switch method {
	case "auto":
		cfg.SetMethod(wifi.IpConfig_METHOD_AUTO)
	case "manual":
		cfg.SetMethod(wifi.IpConfig_METHOD_MANUAL)
	case "disabled":
		cfg.SetMethod(wifi.IpConfig_METHOD_DISABLED)
	default:
		return nil, fmt.Errorf("ip4 method must be auto|manual|disabled (got %q)", method)
	}
	if addr != "" {
		cfg.SetAddresses(splitCSV(addr))
	}
	if gw != "" {
		cfg.SetGateway(gw)
	}
	if dns != "" {
		cfg.SetDns(splitCSV(dns))
	}
	return cfg, nil
}

func splitCSV(s string) []string {
	out := []string{}
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func secStr(s *wifi.Security) string {
	switch s.WhichCredential() {
	case wifi.Security_Psk_case:
		return "psk"
	case wifi.Security_Enterprise_case:
		return "enterprise"
	case wifi.Security_Open_case:
		return "open"
	default:
		return "-"
	}
}
