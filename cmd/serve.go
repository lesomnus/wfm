package cmd

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/lesomnus/otx/log"
	"github.com/lesomnus/xli"
	"github.com/lesomnus/xli/flg"
	"google.golang.org/grpc"

	"github.com/lesomnus/wfm/internal/server"
)

func NewCmdServe() *xli.Command {
	return &xli.Command{
		Name:  "serve",
		Brief: "run the wifi gRPC server (backed by NetworkManager or iwd)",

		Flags: flg.Flags{
			&flg.String{Name: "addr", Brief: "listen address", Value: ptr(":50051")},
			&flg.String{Name: "backend", Brief: "wifi backend: nmcli|nmdbus|iwd (default: autodetect)"},
		},

		Handler: xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
			addr, ok := flg.Get[string](cmd, "addr")
			if !ok || addr == "" {
				addr = ":50051"
			}

			backendName, _ := flg.Get[string](cmd, "backend")
			if backendName == "" {
				backendName = detectBackend()
			}
			b, err := newBackend(backendName)
			if err != nil {
				return err
			}
			defer b.Close()

			lis, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()

			s := grpc.NewServer()
			server.Register(s, b)

			go func() {
				<-ctx.Done()
				s.GracefulStop()
			}()

			log.From(ctx).Info("listen", slog.String("addr", addr), slog.String("backend", backendName))
			return s.Serve(lis)
		}),
	}
}
