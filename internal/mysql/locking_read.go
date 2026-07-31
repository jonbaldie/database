package mysql

import "strings"

// lockingRead records the row lock requested by a SELECT statement. The
// current grammar applies the requested mode to each direct table row that
// remains after the WHERE predicate.
type lockingRead struct {
	mode   lockMode
	policy lockPolicy
}

func splitLockingRead(query string) (string, *lockingRead, error) {
	query = strings.TrimSpace(strings.TrimSuffix(query, ";"))
	lower := strings.ToLower(query)
	for _, candidate := range []struct {
		suffix string
		mode   lockMode
		policy lockPolicy
	}{
		{"for update skip locked", lockExclusive, lockSkip},
		{"for share skip locked", lockShared, lockSkip},
		{"for update nowait", lockExclusive, lockNoWait},
		{"for share nowait", lockShared, lockNoWait},
		{"lock in share mode", lockShared, lockWait},
		{"for update", lockExclusive, lockWait},
		{"for share", lockShared, lockWait},
	} {
		if !strings.HasSuffix(lower, candidate.suffix) {
			continue
		}
		at := len(query) - len(candidate.suffix)
		if at == 0 || !isRelationSpace(query[at-1]) {
			continue
		}
		base := strings.TrimSpace(query[:at])
		if base == "" {
			return "", nil, sqlFailure{1064, "42000", "locking read requires a SELECT"}
		}
		return base, &lockingRead{mode: candidate.mode, policy: candidate.policy}, nil
	}
	return query, nil, nil
}

func (l lockingRead) acquire(plan *relationalSelectPlan, row relationRow) (bool, error) {
	resources, err := l.resources(plan, row)
	if err != nil {
		return false, err
	}
	return plan.session.server.locks.acquire(plan.session, resources, l.mode, l.policy)
}

func (l lockingRead) available(plan *relationalSelectPlan, row relationRow) (bool, error) {
	resources, err := l.resources(plan, row)
	if err != nil {
		return false, err
	}
	return lockResourcesAvailable(plan.session.server.locks, plan.session, resources, l.mode), nil
}

func (l lockingRead) resources(plan *relationalSelectPlan, row relationRow) ([]rowLockResource, error) {
	resources := make([]rowLockResource, 0, len(plan.source.tables))
	for index, table := range plan.source.tables {
		if table.namespace == "" || table.name == "" || index >= len(row.lockKeys) || row.lockKeys[index] == "" {
			continue
		}
		resources = append(resources, rowLockResource{namespace: table.namespace, table: table.name, key: row.lockKeys[index]})
	}
	if len(resources) == 0 {
		return nil, sqlFailure{1235, "42000", "locking reads require direct table rows"}
	}
	return resources, nil
}

func (l lockingRead) explanationMode() string {
	if l.mode == lockShared {
		return "share"
	}
	return "update"
}

func (l lockingRead) explanationWaitPolicy() string {
	switch l.policy {
	case lockNoWait:
		return "nowait"
	case lockSkip:
		return "skip_locked"
	default:
		return "wait"
	}
}
