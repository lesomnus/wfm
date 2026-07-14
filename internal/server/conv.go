package server

import (
	"errors"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lesomnus/wfm/internal/wnet"
)

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

// errToStatusList maps a list/collection error; list endpoints never report
// NotFound for the collection itself.
func errToStatusList(err error) error {
	return status.Errorf(codes.Internal, "%v", err)
}
