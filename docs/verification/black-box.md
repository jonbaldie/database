# Black-box verification spine

Tests under `test/blackbox` launch the built `database` executable and observe only public command output, process exit status, TCP sockets, and HTTP diagnostics. The `test/blackbox` package also provides reusable probes for later conformance suites:

- `Runner` captures command output and exit classes.
- `Process` waits for lifecycle events, requests graceful stops, and records crash-visible exits.
- `HTTPJSON` exercises diagnostics endpoints.
- `ProbeMySQL` reads a classic-protocol greeting without coupling tests to a driver or server package.

The current executable slice proves version reporting, lifecycle readiness, diagnostics responses, graceful restart state, and unclean restart visibility. Later tickets add the MySQL handshake, SQL, durable data, and the complete diagnostics and operator contracts behind these same black-box seams.
