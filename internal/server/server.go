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

// Register wires all four services onto the gRPC server, backed by b.
func Register(s *grpc.Server, b wnet.Backend) {
	wifi.RegisterInterfaceServiceServer(s, &InterfaceServer{b: b})
	wifi.RegisterAccessPointServiceServer(s, &AccessPointServer{b: b})
	wifi.RegisterProfileServiceServer(s, &ProfileServer{b: b})
	wifi.RegisterConnectionServiceServer(s, &ConnectionServer{b: b})
}
