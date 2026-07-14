package cmd

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/lesomnus/otx"
	"github.com/lesomnus/otx/log"
	"github.com/lesomnus/otx/otxgrpc"
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
			&flg.String{Name: "addr", Brief: "listen address (unix socket path or host:port; default: unix socket)"},
			&flg.String{Name: "backend", Brief: "wifi backend: nmcli|nmdbus|iwd (default: autodetect)"},
		},

		Handler: xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
			addr, _ := flg.Get[string](cmd, "addr")

			backendName, _ := flg.Get[string](cmd, "backend")
			if backendName == "" {
				backendName = detectBackend()
			}

			b, err := newBackend(backendName)
			if err != nil {
				return err
			}
			defer b.Close()

			lis, err := listen(addr)
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()

			o := otx.From(ctx)
			s := grpc.NewServer(
				grpc.StatsHandler(otxgrpc.NewServerHandler(o)),
				grpc.StatsHandler(otxgrpc.NewServerLogger()),
			)
			server.Register(s, b)

			go func() {
				<-ctx.Done()
				s.GracefulStop()
			}()

			log.From(ctx).Info("listen", slog.String("addr", lis.Addr().String()), slog.String("backend", backendName))
			return s.Serve(lis)
		}),
	}
}

// listen opens the server listener. An empty or filesystem-looking addr is
// served as a unix-domain socket (default: the shared socket path), creating
// its parent directory and clearing any stale socket file first; anything else
// is treated as a TCP address.
func listen(addr string) (net.Listener, error) {
	if addr == "" {
		addr = defaultSocketPath()
	}
	if isUnixAddr(addr) {
		sock := strings.TrimPrefix(addr, "unix://")
		if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
			return nil, err
		}
		if err := os.Remove(sock); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		return net.Listen("unix", sock)
	}
	return net.Listen("tcp", addr)
}

func isUnixAddr(addr string) bool {
	return strings.HasPrefix(addr, "unix://") ||
		strings.HasPrefix(addr, "/") ||
		strings.HasPrefix(addr, "./") ||
		strings.HasPrefix(addr, "~")
}
