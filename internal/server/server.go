// Package server implements the wifi gRPC services on top of a wnet.Backend.
//
// All proto<->domain mapping, request validation and gRPC status-code
// translation live here, once, so a backend (nmcli, nmdbus, iwd) only has to
// implement wnet.Backend in neutral domain terms. CRUD verbs that are
// meaningless for a projected resource return codes.Unimplemented via the
// embedded Unimplemented*Server stubs.
package server

import (
	"google.golang.org/grpc"

	"github.com/lesomnus/wfm/internal/wifi"
	"github.com/lesomnus/wfm/internal/wnet"
)

// options holds the optional wiring for Register.
type options struct {
	scanCache *ScanCache
}

// Option configures Register.
type Option func(*options)

// WithScanCache makes AccessPointService.Scan serve results from c within its
// TTL instead of re-scanning the radio on every request.
func WithScanCache(c *ScanCache) Option {
	return func(o *options) { o.scanCache = c }
}

// Register wires all four services onto the gRPC server, backed by b.
func Register(s *grpc.Server, b wnet.Backend, opts ...Option) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	wifi.RegisterInterfaceServiceServer(s, &InterfaceServer{b: b})
	wifi.RegisterAccessPointServiceServer(s, &AccessPointServer{b: b, cache: o.scanCache})
	wifi.RegisterProfileServiceServer(s, &ProfileServer{b: b})
	wifi.RegisterConnectionServiceServer(s, &ConnectionServer{b: b})
}
