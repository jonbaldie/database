package mysql

import (
	"net"
	"testing"
	"time"
)

type wrappedConnection struct{ net.Conn }

func TestConversationCloseUnregistersAcceptedConnection(t *testing.T) {
	accepted, peer := net.Pipe()
	defer peer.Close()
	registry := &connectionRegistry{connections: map[net.Conn]struct{}{}, pendingMax: 1, sessionMax: 1}
	if !registry.register(accepted) {
		t.Fatal("register accepted connection")
	}
	conversation := newConversation(&Server{connections: registry}, accepted)
	conversation.connection = wrappedConnection{accepted}
	conversation.close()
	completed := make(chan struct{})
	go func() {
		registry.connectionW.Wait()
		close(completed)
	}()
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("accepted connection remained registered after wrapped connection closed")
	}
}

func TestRegistryLimitsSessionsAfterAuthentication(t *testing.T) {
	first, firstPeer := net.Pipe()
	second, secondPeer := net.Pipe()
	defer firstPeer.Close()
	defer secondPeer.Close()
	registry := &connectionRegistry{connections: map[net.Conn]struct{}{}, pendingMax: 2, sessionMax: 1}
	if !registry.register(first) || !registry.register(second) {
		t.Fatal("unauthenticated connection was rejected")
	}
	if !registry.admitSession() {
		t.Fatal("first authenticated session was rejected")
	}
	if registry.admitSession() {
		t.Fatal("session beyond configured ceiling was admitted")
	}
	registry.unregister(first, true)
	if !registry.admitSession() {
		t.Fatal("released session capacity was not restored")
	}
	registry.unregister(second, true)
}

func TestRegistryLimitsUnauthenticatedConnections(t *testing.T) {
	first, firstPeer := net.Pipe()
	second, secondPeer := net.Pipe()
	defer firstPeer.Close()
	defer secondPeer.Close()
	registry := &connectionRegistry{connections: map[net.Conn]struct{}{}, pendingMax: 1, sessionMax: 1}
	if !registry.register(first) {
		t.Fatal("first connection was rejected")
	}
	if registry.register(second) {
		t.Fatal("connection beyond configured pre-authentication limit was admitted")
	}
	registry.unregister(first, false)
}
