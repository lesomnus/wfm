package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/lesomnus/wfm/internal/wifi"
	"github.com/lesomnus/wfm/internal/wnet"
)

func pbIPConfig(c wnet.IPConfig) *wifi.IpConfig {
	o := &wifi.IpConfig{}
	switch c.Method {
	case wnet.IPAuto:
		o.SetMethod(wifi.IpConfig_METHOD_AUTO)
	case wnet.IPManual:
		o.SetMethod(wifi.IpConfig_METHOD_MANUAL)
	case wnet.IPDisabled:
		o.SetMethod(wifi.IpConfig_METHOD_DISABLED)
	default:
		o.SetMethod(wifi.IpConfig_METHOD_UNSPECIFIED)
	}
	o.SetAddresses(c.Addresses)
	o.SetGateway(c.Gateway)
	o.SetDns(c.DNS)
	o.SetDnsSearch(c.DNSSearch)
	return o
}

func pbSecurity(s wnet.Security) *wifi.Security {
	o := &wifi.Security{}
	switch s.Kind {
	case wnet.SecEnterprise:
		o.SetEnterprise(&wifi.Security_Enterprise{})
	case wnet.SecPSK:
		o.SetPsk(&wifi.Security_Psk{}) // passphrase intentionally not read back
	default:
		o.SetOpen(&wifi.Security_Open{})
	}
	return o
}

func pbProfile(p wnet.Profile) *wifi.Profile {
	o := &wifi.Profile{}
	o.SetId(uuidToBytes(p.ID))
	o.SetName(p.Name)
	o.SetSsid(p.SSID)
	o.SetHidden(p.Hidden)
	o.SetAutoconnect(p.Autoconnect)
	o.SetSecurity(pbSecurity(p.Security))
	o.SetIpv4(pbIPConfig(p.IPv4))
	o.SetIpv6(pbIPConfig(p.IPv6))
	return o
}

func reqSecurity(s *wifi.Security) wnet.Security {
	if s == nil {
		return wnet.Security{Kind: wnet.SecOpen}
	}
	switch s.WhichCredential() {
	case wifi.Security_Psk_case:
		return wnet.Security{Kind: wnet.SecPSK, Passphrase: s.GetPsk().GetPassphrase()}
	case wifi.Security_Enterprise_case:
		return wnet.Security{Kind: wnet.SecEnterprise}
	default:
		return wnet.Security{Kind: wnet.SecOpen}
	}
}

func reqIPConfig(c *wifi.IpConfig) *wnet.IPConfig {
	if c == nil {
		return nil
	}
	cfg := &wnet.IPConfig{
		Addresses: c.GetAddresses(),
		Gateway:   c.GetGateway(),
		DNS:       c.GetDns(),
		DNSSearch: c.GetDnsSearch(),
	}
	switch c.GetMethod() {
	case wifi.IpConfig_METHOD_AUTO:
		cfg.Method = wnet.IPAuto
	case wifi.IpConfig_METHOD_MANUAL:
		cfg.Method = wnet.IPManual
	case wifi.IpConfig_METHOD_DISABLED:
		cfg.Method = wnet.IPDisabled
	default:
		cfg.Method = wnet.IPUnspecified
	}
	return cfg
}

type ProfileServer struct {
	wifi.UnimplementedProfileServiceServer
	b wnet.Backend
}

func (s *ProfileServer) List(ctx context.Context, _ *wifi.ProfileListRequest) (*wifi.ProfileListResponse, error) {
	profs, err := s.b.Profiles(ctx)
	if err != nil {
		return nil, errToStatusList(err)
	}
	items := make([]*wifi.Profile, 0, len(profs))
	for _, p := range profs {
		items = append(items, pbProfile(p))
	}
	resp := &wifi.ProfileListResponse{}
	resp.SetItems(items)
	return resp, nil
}

func (s *ProfileServer) Get(ctx context.Context, req *wifi.ProfileGetRequest) (*wifi.Profile, error) {
	id := uuidFromBytes(req.GetRef().GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid profile ref")
	}
	p, err := s.b.Profile(ctx, id)
	if err != nil {
		return nil, errToStatus("profile "+id, err)
	}
	return pbProfile(p), nil
}

func (s *ProfileServer) Add(ctx context.Context, req *wifi.ProfileAddRequest) (*wifi.Profile, error) {
	if req.GetSsid() == "" {
		return nil, status.Error(codes.InvalidArgument, "ssid is required")
	}
	spec := wnet.ProfileSpec{
		Name:        req.GetName(),
		SSID:        req.GetSsid(),
		Hidden:      req.GetHidden(),
		Autoconnect: req.GetAutoconnect(),
		Security:    reqSecurity(req.GetSecurity()),
		IPv4:        reqIPConfig(req.GetIpv4()),
		IPv6:        reqIPConfig(req.GetIpv6()),
	}
	p, err := s.b.AddProfile(ctx, spec)
	if err != nil {
		return nil, errToStatus("add profile", err)
	}
	return pbProfile(p), nil
}

func (s *ProfileServer) Patch(ctx context.Context, req *wifi.ProfilePatchRequest) (*wifi.Profile, error) {
	id := uuidFromBytes(req.GetRef().GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid profile ref")
	}
	patch := wnet.ProfilePatch{}
	if req.HasName() {
		v := req.GetName()
		patch.Name = &v
	}
	if req.HasSsid() {
		v := req.GetSsid()
		patch.SSID = &v
	}
	if req.HasHidden() {
		v := req.GetHidden()
		patch.Hidden = &v
	}
	if req.HasAutoconnect() {
		v := req.GetAutoconnect()
		patch.Autoconnect = &v
	}
	if req.HasSecurity() {
		sec := reqSecurity(req.GetSecurity())
		patch.Security = &sec
	}
	if req.HasIpv4() {
		patch.IPv4 = reqIPConfig(req.GetIpv4())
	}
	if req.HasIpv6() {
		patch.IPv6 = reqIPConfig(req.GetIpv6())
	}
	p, err := s.b.PatchProfile(ctx, id, patch)
	if err != nil {
		return nil, errToStatus("modify profile", err)
	}
	return pbProfile(p), nil
}

func (s *ProfileServer) Erase(ctx context.Context, ref *wifi.ProfileRef) (*emptypb.Empty, error) {
	id := uuidFromBytes(ref.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid profile ref")
	}
	if err := s.b.DeleteProfile(ctx, id); err != nil {
		return nil, errToStatus("delete profile", err)
	}
	return &emptypb.Empty{}, nil
}
