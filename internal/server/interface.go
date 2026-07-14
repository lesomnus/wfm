package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lesomnus/wfm/internal/wifi"
	"github.com/lesomnus/wfm/internal/wnet"
)

func pbInterface(it wnet.Interface) *wifi.Interface {
	o := &wifi.Interface{}
	o.SetId(it.Name)
	o.SetName(it.Name)
	o.SetDesc(it.Desc)
	o.SetMac(it.Mac)
	o.SetPowered(it.Powered)
	o.SetUp(it.Up)
	return o
}

type InterfaceServer struct {
	wifi.UnimplementedInterfaceServiceServer
	b wnet.Backend
}

func (s *InterfaceServer) List(ctx context.Context, _ *wifi.InterfaceListRequest) (*wifi.InterfaceListResponse, error) {
	its, err := s.b.Interfaces(ctx)
	if err != nil {
		return nil, errToStatusList(err)
	}
	items := make([]*wifi.Interface, 0, len(its))
	for _, it := range its {
		items = append(items, pbInterface(it))
	}
	resp := &wifi.InterfaceListResponse{}
	resp.SetItems(items)
	return resp, nil
}

func (s *InterfaceServer) Get(ctx context.Context, req *wifi.InterfaceGetRequest) (*wifi.Interface, error) {
	name := req.GetRef().GetId()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "interface id required")
	}
	it, err := s.b.Interface(ctx, name)
	if err != nil {
		return nil, errToStatus("interface "+name, err)
	}
	return pbInterface(it), nil
}

func (s *InterfaceServer) SetPower(ctx context.Context, req *wifi.InterfaceSetPowerRequest) (*wifi.Interface, error) {
	name := req.GetRef().GetId()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "interface id required")
	}
	it, err := s.b.SetPower(ctx, name, req.GetOn())
	if err != nil {
		return nil, errToStatus("set power", err)
	}
	return pbInterface(it), nil
}
