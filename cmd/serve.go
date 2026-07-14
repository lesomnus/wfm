package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lesomnus/otx"
	"github.com/lesomnus/otx/log"
	"github.com/lesomnus/otx/otxgrpc"
	"github.com/lesomnus/xli"
	"github.com/lesomnus/xli/flg"
	"google.golang.org/grpc"

	"github.com/lesomnus/wfm/internal/server"
)

// defaultScanCachePath is where the server persists cached scans, relative to
// its working directory; defaultScanCacheTTL is how long a cached scan is served
// before a new request scans the radio again. It is short: a scan request should
// normally scan, and the cache only collapses bursts within this window.
const (
	defaultScanCachePath = ".wfm"
	defaultScanCacheTTL  = 5 * time.Second
)

func NewCmdServe() *xli.Command {
	return &xli.Command{
		Name:  "serve",
		Brief: "run the wifi gRPC server (backed by NetworkManager or iwd)",

		Flags: flg.Flags{
			&flg.String{Name: "addr", Brief: "listen address (unix socket path or host:port; default: unix socket)"},
			&flg.String{Name: "backend", Brief: "wifi backend: nmcli|nmdbus|iwd (default: autodetect)"},
			&flg.String{Name: "scan-cache", Brief: "path to the scan cache file (default: .wfm)"},
			&flg.String{Name: "scan-cache-ttl", Brief: "how long a cached scan is served before rescanning, e.g. 5s; 0 disables (default: 5s)"},
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

			regOpts, err := scanCacheOpts(ctx, cmd)
			if err != nil {
				return err
			}

			o := otx.From(ctx)
			s := grpc.NewServer(
				grpc.StatsHandler(otxgrpc.NewServerHandler(o)),
				grpc.StatsHandler(otxgrpc.NewServerLogger()),
			)
			server.Register(s, b, regOpts...)

			go func() {
				<-ctx.Done()
				s.GracefulStop()
			}()

			log.From(ctx).Info("listen", slog.String("addr", lis.Addr().String()), slog.String("backend", backendName))
			return s.Serve(lis)
		}),
	}
}

// scanCacheOpts builds the Register options for the access-point scan cache from
// the --scan-cache/--scan-cache-ttl flags. A TTL of 0 disables the cache. A
// failure to load an existing cache is logged, not fatal.
func scanCacheOpts(ctx context.Context, cmd *xli.Command) ([]server.Option, error) {
	ttlStr, _ := flg.Get[string](cmd, "scan-cache-ttl")
	ttl := defaultScanCacheTTL
	if ttlStr != "" {
		d, err := time.ParseDuration(ttlStr)
		if err != nil {
			return nil, fmt.Errorf("invalid --scan-cache-ttl: %w", err)
		}
		ttl = d
	}
	if ttl <= 0 {
		return nil, nil // caching disabled
	}

	path, _ := flg.Get[string](cmd, "scan-cache")
	if path == "" {
		path = defaultScanCachePath
	}

	c := server.NewScanCache(path, ttl)
	if err := c.Load(time.Now()); err != nil {
		log.From(ctx).Warn("scan cache load failed", slog.Any("err", err), slog.String("path", path))
	}
	log.From(ctx).Info("scan cache enabled", slog.String("path", path), slog.Duration("ttl", ttl))
	return []server.Option{server.WithScanCache(c)}, nil
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
