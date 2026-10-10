package mysql

import (
	"testing"

	"github.com/jonbaldie/database/internal/buildinfo"
)

func TestServerVersionDefaultsToBuildIdentity(t *testing.T) {
	for _, test := range []struct {
		name    string
		version string
		probe   bool
	}{
		{name: "configured default"},
		{name: "protocol probe", probe: true},
		{name: "explicit override", version: "test-server-version"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var server *Server
			var err error
			if test.probe {
				server, err = New("127.0.0.1:0")
			} else {
				server, err = NewWithConfig("127.0.0.1:0", Config{Version: test.version})
			}
			if err != nil {
				t.Fatal(err)
			}
			defer server.Listener.Close()
			want := test.version
			if want == "" {
				want = buildinfo.ProductVersion
			}
			if server.config.Version != want {
				t.Fatalf("server version = %q, want %q", server.config.Version, want)
			}
		})
	}
}
