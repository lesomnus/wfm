package server

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/lesomnus/wfm/internal/wifi"
	"github.com/lesomnus/wfm/internal/wnet"
)

func pbState(s wnet.ConnState) wifi.ConnectionState {
	switch s {
	case wnet.StateIdle:
		return wifi.ConnectionState_CONNECTION_STATE_IDLE
	case wnet.StateAssociating:
		return wifi.ConnectionState_CONNECTION_STATE_ASSOCIATING
	case wnet.StateAuthenticating:
		return wifi.ConnectionState_CONNECTION_STATE_AUTHENTICATING
	case wnet.StateConfiguring:
		return wifi.ConnectionState_CONNECTION_STATE_CONFIGURING
	case wnet.StateConnected:
		return wifi.ConnectionState_CONNECTION_STATE_CONNECTED
	case wnet.StateFailed:
		return wifi.ConnectionState_CONNECTION_STATE_FAILED
	case wnet.StateDisconnecting:
		return wifi.ConnectionState_CONNECTION_STATE_DISCONNECTING
	default:
		return wifi.ConnectionState_CONNECTION_STATE_UNSPECIFIED
	}
}

func pbConnError(e wnet.ConnError) wifi.ConnectionError {
	switch e {
	case wnet.ErrCAuthFailed:
		return wifi.ConnectionError_CONNECTION_ERROR_AUTH_FAILED
	case wnet.ErrCUnknown:
		return wifi.ConnectionError_CONNECTION_ERROR_UNKNOWN
	default:
		return wifi.ConnectionError_CONNECTION_ERROR_NONE
	}
}

func pbStatus(st wnet.Status) *wifi.ConnectionStatus {
	o := &wifi.ConnectionStatus{}
	o.SetState(pbState(st.State))
	o.SetError(pbConnError(st.Error))
	if st.Detail != "" {
		o.SetDetail(st.Detail)
	}
	if st.BSSID != "" {
		o.SetBssid(st.BSSID)
		o.SetSignal(int32(st.Signal))
	}
	o.SetAddresses(st.Addresses)
	o.SetGateway(st.Gateway)
	o.SetDns(st.DNS)
	return o
}

type ConnectionServer struct {
	wifi.UnimplementedConnectionServiceServer
	b wnet.Backend
}

func (s *ConnectionServer) pbConnection(ctx context.Context, a wnet.Active) *wifi.Connection {
	c := &wifi.Connection{}
	c.SetId(uuidToBytes(a.ID))
	if it, err := s.b.Interface(ctx, a.Iface); err == nil {
		c.SetInterface(pbInterface(it))
	}
	if p, err := s.b.Profile(ctx, a.ProfileID); err == nil {
		c.SetProfile(pbProfile(p))
	}
	if a.SSID != "" || a.BSSID != "" {
		ap := &wifi.AccessPoint{}
		ap.SetId(a.BSSID)
		ap.SetSsid(a.SSID)
		ap.SetBssid(a.BSSID)
		c.SetAccessPoint(ap)
	}
	return c
}

func (s *ConnectionServer) Add(ctx context.Context, req *wifi.ConnectionAddRequest) (*wifi.Connection, error) {
	ifname := req.GetInterface().GetId()
	prof := uuidFromBytes(req.GetProfile().GetId())
	if ifname == "" || prof == "" {
		return nil, status.Error(codes.InvalidArgument, "interface and profile are required")
	}
	bssid := ""
	if ap := req.GetAccessPoint(); ap != nil {
		bssid = ap.GetId()
	}
	a, err := s.b.Activate(ctx, ifname, prof, bssid)
	if err != nil {
		return nil, errToStatus("activate", err)
	}
	return s.pbConnection(ctx, a), nil
}

func (s *ConnectionServer) Get(ctx context.Context, req *wifi.ConnectionGetRequest) (*wifi.Connection, error) {
	id := uuidFromBytes(req.GetRef().GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid connection ref")
	}
	a, err := s.b.Connection(ctx, id)
	if err != nil {
		return nil, errToStatus("connection "+id, err)
	}
	return s.pbConnection(ctx, a), nil
}

func (s *ConnectionServer) List(ctx context.Context, req *wifi.ConnectionListRequest) (*wifi.ConnectionListResponse, error) {
	actives, err := s.b.Connections(ctx)
	if err != nil {
		return nil, errToStatusList(err)
	}
	ifaceFilter := req.GetInterface().GetId() // optional: only this interface's connections
	items := make([]*wifi.Connection, 0, len(actives))
	for _, a := range actives {
		if ifaceFilter != "" && a.Iface != ifaceFilter {
			continue
		}
		items = append(items, s.pbConnection(ctx, a))
	}
	resp := &wifi.ConnectionListResponse{}
	resp.SetItems(items)
	return resp, nil
}

func (s *ConnectionServer) Erase(ctx context.Context, ref *wifi.ConnectionRef) (*emptypb.Empty, error) {
	id := uuidFromBytes(ref.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid connection ref")
	}
	if err := s.b.Deactivate(ctx, id); err != nil {
		return nil, errToStatus("deactivate", err)
	}
	return &emptypb.Empty{}, nil
}

func (s *ConnectionServer) GetStatus(ctx context.Context, ref *wifi.ConnectionRef) (*wifi.ConnectionStatus, error) {
	id := uuidFromBytes(ref.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid connection ref")
	}
	st, err := s.b.Status(ctx, id)
	if err != nil {
		return nil, errToStatus("status", err)
	}
	return pbStatus(st), nil
}

func (s *ConnectionServer) Watch(ref *wifi.ConnectionRef, stream grpc.ServerStreamingServer[wifi.ConnectionStatus]) error {
	id := uuidFromBytes(ref.GetId())
	if id == "" {
		return status.Error(codes.InvalidArgument, "invalid connection ref")
	}
	ch, err := s.b.Watch(stream.Context(), id)
	if err != nil {
		return errToStatus("watch", err)
	}
	for st := range ch {
		if err := stream.Send(pbStatus(st)); err != nil {
			return err
		}
	}
	return nil
}
