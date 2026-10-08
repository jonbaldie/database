package mysql

import (
	"io"
	"time"
)

// IdleTimeouts bounds how long a session may wait between client commands.
// InTransaction applies while the session has an open transaction; expiry
// rolls back the transaction and closes the session. Session applies
// otherwise; expiry closes the session.
type IdleTimeouts struct {
	InTransaction time.Duration
	Session       time.Duration
}

func normalizedIdleTimeouts(timeouts IdleTimeouts) IdleTimeouts {
	if timeouts.InTransaction <= 0 {
		timeouts.InTransaction = 5 * time.Minute
	}
	if timeouts.Session <= 0 {
		timeouts.Session = time.Hour
	}
	return timeouts
}

// await returns the next client command. It returns nil when the client
// stops or when the applicable idle timeout expires first. On expiry it rolls
// back an open transaction and reports the inactivity disconnect to the
// client before the caller closes the session.
func (t IdleTimeouts) await(s *session, client io.Writer, watch *statementWatch) *pendingCommand {
	timeout, message := t.Session, "The client was disconnected by the server because of inactivity. See idle_session_timeout_ms."
	if s.transaction {
		timeout, message = t.InTransaction, "The client was disconnected by the server because of inactivity in an open transaction; the transaction was rolled back. See idle_in_transaction_timeout_ms."
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case pending := <-watch.finished:
		return pending
	case <-timer.C:
		_ = rollbackTransaction(s)
		_ = writePacket(client, 0, errorPacket(4031, "HY000", message))
		return nil
	}
}
