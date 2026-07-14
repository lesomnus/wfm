package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/lesomnus/xli"
	"github.com/lesomnus/xli/arg"

	"github.com/lesomnus/wfm/internal/wifi"
)

// NewCmdConnect is the headline convenience command implementing the plan
// scenario: pick an interface + SSID, supply the passphrase, and wait for the
// connection. It creates a profile, activates it on the interface, and reports
// the result (the server blocks until connected or failed).
func NewCmdConnect() *xli.Command {
	return &xli.Command{
		Name:  "connect",
		Brief: "create a profile and connect an interface to an SSID",
		Args: arg.Args{
			&arg.String{Name: "interface"},
			&arg.String{Name: "ssid"},
			&arg.String{Name: "psk", Optional: true},
		},
		Handler: xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
			ifname, _ := arg.Get[string](cmd, "interface")
			ssid, _ := arg.Get[string](cmd, "ssid")
			psk, _ := arg.Get[string](cmd, "psk")
			if ifname == "" || ssid == "" {
				return fmt.Errorf("usage: connect <interface> <ssid> [psk]")
			}

			cc, err := dial(ctx, cmd)
			if err != nil {
				return err
			}
			defer cc.Close()

			profiles := wifi.NewProfileServiceClient(cc)
			conns := wifi.NewConnectionServiceClient(cc)

			// 1. create the profile (the saved settings record).
			add := &wifi.ProfileAddRequest{}
			add.SetSsid(ssid)
			add.SetAutoconnect(true)
			sec := &wifi.Security{}
			if psk != "" {
				p := &wifi.Security_Psk{}
				p.SetPassphrase(psk)
				sec.SetPsk(p)
			} else {
				sec.SetOpen(&wifi.Security_Open{})
			}
			add.SetSecurity(sec)

			prof, err := profiles.Add(ctx, add)
			if err != nil {
				return fmt.Errorf("create profile: %w", err)
			}
			cmd.Printf("profile %s created for %q\n", uuidStr(prof.GetId()), ssid)

			// 2. activate the profile on the interface and wait.
			cmd.Printf("connecting %s -> %q ...\n", ifname, ssid)
			creq := &wifi.ConnectionAddRequest{}
			iref := &wifi.InterfaceRef{}
			iref.SetId(ifname)
			creq.SetInterface(iref)
			pref := &wifi.ProfileRef{}
			pref.SetId(prof.GetId())
			creq.SetProfile(pref)

			conn, err := conns.Add(ctx, creq)
			if err != nil {
				// Activation failed (e.g. wrong passphrase); drop the profile we just made.
				pr := &wifi.ProfileRef{}
				pr.SetId(prof.GetId())
				_, _ = profiles.Erase(ctx, pr)
				return fmt.Errorf("connect failed (profile rolled back): %w", err)
			}

			// 3. report the result.
			st, err := conns.GetStatus(ctx, refOfConn(conn))
			if err == nil {
				cmd.Printf("connected: %s\n", connectionSummary(conn, st))
			} else {
				cmd.Printf("connected: %s\n", uuidStr(conn.GetId()))
			}
			return nil
		}),
	}
}

func NewCmdConnection() *xli.Command {
	return &xli.Command{
		Name:    "connection",
		Aliases: []string{"conn"},
		Brief:   "inspect and control active connections",

		Commands: []*xli.Command{
			{
				Name:    "list",
				Aliases: []string{"ls"},
				Brief:   "list active connections",
				Handler: xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
					cc, err := dial(ctx, cmd)
					if err != nil {
						return err
					}
					defer cc.Close()

					resp, err := wifi.NewConnectionServiceClient(cc).List(ctx, &wifi.ConnectionListRequest{})
					if err != nil {
						return err
					}
					for _, c := range resp.GetItems() {
						cmd.Printf("%s  iface=%-8s ssid=%-24s bssid=%s\n",
							uuidStr(c.GetId()), c.GetInterface().GetId(), c.GetProfile().GetSsid(), c.GetAccessPoint().GetBssid())
					}
					return nil
				}),
			},
			{
				Name:  "status",
				Brief: "show the current status of a connection",
				Args:  arg.Args{&arg.String{Name: "uuid"}},
				Handler: xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
					ref, err := connRefArg(cmd)
					if err != nil {
						return err
					}
					cc, err := dial(ctx, cmd)
					if err != nil {
						return err
					}
					defer cc.Close()

					st, err := wifi.NewConnectionServiceClient(cc).GetStatus(ctx, ref)
					if err != nil {
						return err
					}
					cmd.Println(statusLine(st))
					return nil
				}),
			},
			{
				Name:  "watch",
				Brief: "stream status changes until the connection settles",
				Args:  arg.Args{&arg.String{Name: "uuid"}},
				Handler: xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
					ref, err := connRefArg(cmd)
					if err != nil {
						return err
					}
					cc, err := dial(ctx, cmd)
					if err != nil {
						return err
					}
					defer cc.Close()

					stream, err := wifi.NewConnectionServiceClient(cc).Watch(ctx, ref)
					if err != nil {
						return err
					}
					for {
						st, err := stream.Recv()
						if err == io.EOF {
							return nil
						}
						if err != nil {
							return err
						}
						cmd.Println(statusLine(st))
					}
				}),
			},
			{
				Name:  "down",
				Brief: "deactivate a connection",
				Args:  arg.Args{&arg.String{Name: "uuid"}},
				Handler: xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
					ref, err := connRefArg(cmd)
					if err != nil {
						return err
					}
					cc, err := dial(ctx, cmd)
					if err != nil {
						return err
					}
					defer cc.Close()

					if _, err := wifi.NewConnectionServiceClient(cc).Erase(ctx, ref); err != nil {
						return err
					}
					cmd.Println("deactivated")
					return nil
				}),
			},
		},

		Handler: xli.RequireSubcommand(),
	}
}

func connRefArg(cmd *xli.Command) (*wifi.ConnectionRef, error) {
	id, _ := arg.Get[string](cmd, "uuid")
	b, err := uuidBytes(id)
	if err != nil {
		return nil, fmt.Errorf("invalid uuid: %w", err)
	}
	ref := &wifi.ConnectionRef{}
	ref.SetId(b)
	return ref, nil
}

func refOfConn(c *wifi.Connection) *wifi.ConnectionRef {
	ref := &wifi.ConnectionRef{}
	ref.SetId(c.GetId())
	return ref
}

func statusLine(st *wifi.ConnectionStatus) string {
	line := fmt.Sprintf("state=%s", stateStr(st.GetState()))
	if st.GetError() != wifi.ConnectionError_CONNECTION_ERROR_NONE &&
		st.GetError() != wifi.ConnectionError_CONNECTION_ERROR_UNSPECIFIED {
		line += fmt.Sprintf(" error=%s", st.GetError().String())
	}
	if st.GetBssid() != "" {
		line += fmt.Sprintf(" bssid=%s sig=%d", st.GetBssid(), st.GetSignal())
	}
	if len(st.GetAddresses()) > 0 {
		line += fmt.Sprintf(" addr=%s", strings.Join(st.GetAddresses(), ","))
	}
	if st.GetGateway() != "" {
		line += fmt.Sprintf(" gw=%s", st.GetGateway())
	}
	if len(st.GetDns()) > 0 {
		line += fmt.Sprintf(" dns=%s", strings.Join(st.GetDns(), ","))
	}
	if st.GetDetail() != "" {
		line += fmt.Sprintf(" (%s)", st.GetDetail())
	}
	return line
}

func connectionSummary(c *wifi.Connection, st *wifi.ConnectionStatus) string {
	return fmt.Sprintf("%s iface=%s %s", uuidStr(c.GetId()), c.GetInterface().GetId(), statusLine(st))
}
