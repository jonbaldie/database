package mysql

import "testing"

// testSessionOptions names the only session state a unit test may choose.
// Every other field comes from newSession and the server configuration.
type testSessionOptions struct {
	// account is the authenticated account name.
	account string
	// database is the database named at authentication.
	database string
	// connectionID is the server-assigned connection identifier.
	connectionID uint32
	// resources is the statement resource scope. Production assigns it when a
	// statement starts, so a test that calls an evaluator directly sets it here.
	resources *statementResources
}

// newTestSession builds a session the way a successful handshake does: it
// calls newSession with an authentication result for options.account and
// options.database, so collation, time zone, lock wait, and resource limits
// come from server.config. It then applies only the overrides in options.
func newTestSession(server *Server, options testSessionOptions) *session {
	authentication := authenticationResult{accountName: options.account, database: options.database}
	session := newSession(server, authentication, options.connectionID)
	session.resources = options.resources
	return session
}

// newTestServer builds a server through NewWithConfig, so the configuration
// passes through normalizedConfig, and closes its listener when the test ends.
func newTestServer(tb testing.TB, config Config) *Server {
	tb.Helper()
	server, err := NewWithConfig("127.0.0.1:0", config)
	if err != nil {
		tb.Fatalf("new server: %v", err)
	}
	tb.Cleanup(func() { _ = server.Listener.Close() })
	return server
}
