package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		address string
		want    bool
	}{
		{"localhost:3306", true},
		{"localhost:0", true},
		{"127.0.0.1:3306", true},
		{"127.0.0.2:3306", true},
		{"[::1]:3306", true},
		{"0.0.0.0:3306", false},
		{"[::]:3306", false},
		{"192.168.1.1:3306", false},
		{"example.com:3306", false},
		{"malformed-address", false},
		{":::", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			if got := IsLoopback(tt.address); got != tt.want {
				t.Errorf("IsLoopback(%q) = %v, want %v", tt.address, got, tt.want)
			}
		})
	}
}

func TestServeDoesNotWarnForLocalhostMySQLWithoutTLS(t *testing.T) {
	directory := initializedDirectory(t)
	ctx, stop := context.WithCancel(context.Background())
	events := make(chan Event, 4)
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Options{
			DataDirectory: directory,
			MySQLAddress:  "localhost:0",
			MySQLEnabled:  true,
		}, func(event Event) { events <- event })
	}()
	ready := receiveEvent(t, events, "ready")
	if len(ready.Warnings) != 0 {
		stop()
		<-done
		t.Fatalf("localhost ready warnings = %#v, want none", ready.Warnings)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatalf("Serve shutdown: %v", err)
	}
}

func TestStartFailureClassificationRequiresSentinelError(t *testing.T) {
	unrelatedErr := errors.New("query failed: table damaged_items does not exist; corruption check skipped; recover catalog")
	if reason := startFailureReason(unrelatedErr); reason != "starting" {
		t.Fatalf("startFailureReason(unrelatedErr) = %q, want %q", reason, "starting")
	}
	if code := startFailureCode(unrelatedErr); code != "server.start_failed" {
		t.Fatalf("startFailureCode(unrelatedErr) = %q, want %q", code, "server.start_failed")
	}

	corruptDiagnostic := "disk read failure at block 42"
	corruptErr := fmt.Errorf("%w: %s", catalog.ErrCorrupt, corruptDiagnostic)
	if !errors.Is(corruptErr, catalog.ErrCorrupt) {
		t.Fatalf("errors.Is(corruptErr, catalog.ErrCorrupt) = false, want true")
	}
	if reason := startFailureReason(corruptErr); reason != "corruption" {
		t.Fatalf("startFailureReason(corruptErr) = %q, want %q", reason, "corruption")
	}
	if code := startFailureCode(corruptErr); code != "corruption.detected" {
		t.Fatalf("startFailureCode(corruptErr) = %q, want %q", code, "corruption.detected")
	}
	if !strings.Contains(corruptErr.Error(), corruptDiagnostic) {
		t.Fatalf("corruptErr.Error() = %q, want diagnostic %q", corruptErr.Error(), corruptDiagnostic)
	}
}

func TestValidateInstanceCatalogRejectsDamagedCatalogWithSentinel(t *testing.T) {
	directory := initializedDirectory(t)
	if err := os.WriteFile(filepath.Join(directory, "catalog.json"), []byte(`{"namespaces":{"bad":null}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := validateInstanceCatalog(directory)
	if err == nil {
		t.Fatal("validateInstanceCatalog succeeded on damaged catalog, want error")
	}
	if !errors.Is(err, catalog.ErrCorrupt) {
		t.Fatalf("validateInstanceCatalog error = %v, want catalog.ErrCorrupt", err)
	}
}
