package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

// executable is a database binary built once for the server lifecycle tests.
var executable string

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "database-performance-test-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	executable = filepath.Join(directory, "database")
	build := exec.Command("go", "build", "-o", executable, "../../cmd/database")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	code := 1
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build database:", err)
	} else {
		code = m.Run()
	}
	_ = os.RemoveAll(directory)
	os.Exit(code)
}

func TestStartServerPingsTheReadyServerAndStopsIt(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "data")
	if err := initialize(executable, directory); err != nil {
		t.Fatal(err)
	}
	server, db, startup, err := startServer(executable, directory)
	if err != nil {
		t.Fatal(err)
	}
	if startup <= 0 || server.address != server.instance.MySQLAddress {
		t.Fatalf("startup = %v, address = %q, ready address = %q", startup, server.address, server.instance.MySQLAddress)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping after start: %v", err)
	}
	_ = db.Close()
	if err := server.stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	crashed, db, _, err := startServer(executable, directory)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	_ = crashed.instance.Process.Crash()
	if err := crashed.stop(); err == nil || !strings.Contains(err.Error(), "wait for server") {
		t.Fatalf("stop after crash: error = %v, want the failed wait", err)
	}
	if err := (*runningServer)(nil).stop(); err != nil {
		t.Fatalf("stop of no server: %v", err)
	}
}

func TestStartServerFailsWhenTheServerIsNotReadyOrRejectsTheSession(t *testing.T) {
	if _, _, _, err := startServer(executable, filepath.Join(t.TempDir(), "missing")); err == nil || !strings.Contains(err.Error(), "wait for ready event") {
		t.Fatalf("uninitialized directory: error = %v", err)
	}
	directory := filepath.Join(t.TempDir(), "data")
	if err := blackbox.Initialize(context.Background(), blackbox.Runner{Executable: executable}, directory, "another-password"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := startServer(executable, directory); err == nil || !strings.Contains(err.Error(), "ping ready server") || !strings.Contains(err.Error(), "server stop: <nil>") {
		t.Fatalf("wrong password: error = %v, want a failed ping and a clean stop", err)
	}
}
