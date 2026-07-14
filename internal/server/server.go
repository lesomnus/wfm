// Package server implements the wifi gRPC services on top of a wnet.Backend.
//
// All proto<->domain mapping, request validation and gRPC status-code
// translation live here, once, so a backend (nmcli, nmdbus, iwd) only has to
// implement wnet.Backend in neutral domain terms. CRUD verbs that are
// meaningless for a projected resource return codes.Unimplemented via the
// embedded Unimplemented*Server stubs.
package server

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	wifi "github.com/lesomnus/wfm/internal/wifi"
	"github.com/lesomnus/wfm/internal/wnet"
)

// Register wires all four services onto the gRPC server, backed by b.
func Register(s *grpc.Server, b wnet.Backend) {
	wifi.RegisterInterfaceServiceServer(s, &InterfaceServer{b: b})
	wifi.RegisterAccessPointServiceServer(s, &AccessPointServer{b: b})
	wifi.RegisterProfileServiceServer(s, &ProfileServer{b: b})
	wifi.RegisterConnectionServiceServer(s, &ConnectionServer{b: b})
}

// ---- shared conversions -----------------------------------------------------

func uuidToBytes(s string) []byte {
	u, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	b, _ := u.MarshalBinary()
	return b
}

func uuidFromBytes(b []byte) string {
	u, err := uuid.FromBytes(b)
	if err != nil {
		return ""
	}
	return u.String()
}

// errToStatus maps backend errors to gRPC status codes.
func errToStatus(action string, err error) error {
	switch {
	case errors.Is(err, wnet.ErrNotFound):
		return status.Errorf(codes.NotFound, "%s: %v", action, err)
	case errors.Is(err, wnet.ErrUnsupported):
		return status.Errorf(codes.Unimplemented, "%s: %v", action, err)
	default:
		return status.Errorf(codes.Internal, "%s: %v", action, err)
	}
}

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

func pbKeyMgmt(k wnet.KeyMgmt) wifi.KeyManagement {
	switch k {
	case wnet.KeyWPAPSK:
		return wifi.KeyManagement_KEY_MANAGEMENT_WPA_PSK
	case wnet.KeySAE:
		return wifi.KeyManagement_KEY_MANAGEMENT_SAE
	case wnet.KeyWPAEAP:
		return wifi.KeyManagement_KEY_MANAGEMENT_WPA_EAP
	case wnet.KeyOWE:
		return wifi.KeyManagement_KEY_MANAGEMENT_OWE
	default:
		return wifi.KeyManagement_KEY_MANAGEMENT_NONE
	}
}

func pbKeyMgmts(ks []wnet.KeyMgmt) []wifi.KeyManagement {
	out := make([]wifi.KeyManagement, 0, len(ks))
	for _, k := range ks {
		out = append(out, pbKeyMgmt(k))
	}
	return out
}

func pbAccessPoint(ap wnet.AP) *wifi.AccessPoint {
	o := &wifi.AccessPoint{}
	o.SetId(ap.BSSID)
	o.SetSsid(ap.SSID)
	o.SetBssid(ap.BSSID)
	o.SetSignal(int32(ap.Signal))
	o.SetFrequency(ap.FreqMHz)
	o.SetKeyManagement(pbKeyMgmts(ap.KeyMgmt))
	return o
}

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

// ---- InterfaceService -------------------------------------------------------

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

// ---- AccessPointService -----------------------------------------------------

type AccessPointServer struct {
	wifi.UnimplementedAccessPointServiceServer
	b wnet.Backend
}

func (s *AccessPointServer) Scan(ctx context.Context, req *wifi.AccessPointScanRequest) (*wifi.AccessPointScanResponse, error) {
	ifname := req.GetInterface().GetId()
	if ifname == "" {
		return nil, status.Error(codes.InvalidArgument, "interface id required")
	}
	aps, err := s.b.Scan(ctx, ifname)
	if err != nil {
		return nil, errToStatus("scan", err)
	}
	items := make([]*wifi.AccessPoint, 0, len(aps))
	for _, ap := range aps {
		items = append(items, pbAccessPoint(ap))
	}
	resp := &wifi.AccessPointScanResponse{}
	resp.SetItems(items)
	return resp, nil
}

// ---- ProfileService ---------------------------------------------------------

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

// ---- ConnectionService ------------------------------------------------------

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

// errToStatusList maps a list/collection error; list endpoints never report
// NotFound for the collection itself.
func errToStatusList(err error) error {
	return status.Errorf(codes.Internal, "%v", err)
}
