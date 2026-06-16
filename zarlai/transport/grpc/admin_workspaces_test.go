package grpc

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"

	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
)

func TestAdminServer_UpsertWorkspace_validation(t *testing.T) {
	s := &AdminServer{} // repo nil — validation should short-circuit before repo access

	cases := []struct {
		name    string
		req     *zarlv1.UpsertWorkspaceRequest
		wantErr bool
	}{
		{"empty name", &zarlv1.UpsertWorkspaceRequest{Name: "", Root: "/tmp"}, true},
		{"bad name chars", &zarlv1.UpsertWorkspaceRequest{Name: "Bad Name!", Root: "/tmp"}, true},
		{"uppercase name", &zarlv1.UpsertWorkspaceRequest{Name: "Foo", Root: "/tmp"}, true},
		{"relative root", &zarlv1.UpsertWorkspaceRequest{Name: "acme", Root: "relative/path"}, true},
		{"empty root", &zarlv1.UpsertWorkspaceRequest{Name: "acme", Root: ""}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.UpsertWorkspace(context.Background(), connect.NewRequest(tc.req))
			if err == nil {
				t.Errorf("expected validation error, got nil")
				return
			}
			var ce *connect.Error
			if !errors.As(err, &ce) {
				t.Errorf("err type = %T, want *connect.Error", err)
				return
			}
			if ce.Code() != connect.CodeInvalidArgument {
				t.Errorf("err code = %v, want InvalidArgument", ce.Code())
			}
		})
	}
}
