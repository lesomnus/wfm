package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lesomnus/wfm/internal/wifi"
	"github.com/lesomnus/wfm/internal/wnet"
)

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
