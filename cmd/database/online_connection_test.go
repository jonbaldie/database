package main

import (
	"reflect"
	"testing"

	"github.com/jonbaldie/database/internal/lifecycle"
)

func TestOnlineNonLoopbackWarningAgreesWithLifecycle(t *testing.T) {
	testAddresses := []string{
		"localhost:3306",
		"localhost:0",
		"127.0.0.1:3306",
		"127.0.0.2:3306",
		"[::1]:3306",
		"0.0.0.0:3306",
		"[::]:3306",
		"192.168.1.1:3306",
		"example.com:3306",
		"malformed-address",
		":::",
		"",
	}

	for _, address := range testAddresses {
		t.Run(address, func(t *testing.T) {
			isLoopback := lifecycle.IsLoopback(address)
			warning := onlineNonLoopbackWarning(address)

			if isLoopback && warning != nil {
				t.Errorf("address %q is loopback, but got online warning: %#v", address, warning)
			}
			if !isLoopback && warning == nil {
				t.Errorf("address %q is non-loopback, but got nil online warning", address)
			}
			if !isLoopback && warning != nil {
				want := map[string]string{"address": address, "tls": "disabled"}
				if !reflect.DeepEqual(warning, want) {
					t.Errorf("onlineNonLoopbackWarning(%q) = %#v, want %#v", address, warning, want)
				}
			}
		})
	}
}
