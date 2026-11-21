//go:build rowlockschedule

package mysql

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jonbaldie/database/internal/catalog"
)

// TestRowLockSchedule visits every reachable state of three sessions and one
// row. Each step is one call to waitAttempt, lockNowait, skipLocked, or release.
func TestRowLockSchedule(t *testing.T) {
	const (
		phaseIdle uint8 = iota
		phaseWaiting
		phaseDeadlock
	)
	table := catalog.NewTableRef("app", "items")
	rows := []rowLockResource{
		{table: table, key: "1"},
	}
	sessions := []*session{{}, {}, {}}
	names := []string{"session 1", "session 2", "session 3"}

	type control struct {
		phase     [3]uint8
		want      [3]uint8
		mode      [3]lockMode
		justified [3]bool
	}
	type snap struct {
		grant [3][2]uint8
		wait  [3]uint8
		control
	}
	type step struct {
		kind    string
		session int
		rows    uint8
		mode    lockMode
		result  string
	}
	type node struct {
		parent int
		step   step
		state  snap
	}

	manager := newLockManager(0)
	var start snap
	for index := range start.justified {
		start.justified[index] = true
	}
	write := func(state snap) {
		manager.mu.Lock()
		manager.granted = map[rowLockResource]map[*session]lockMode{}
		manager.held = map[*session]map[rowLockResource]lockMode{}
		manager.waiting = map[*session]map[*session]struct{}{}
		manager.waitingSince = map[*session]time.Time{}
		for index, holder := range sessions {
			for row, resource := range rows {
				mode := lockMode(state.grant[index][row])
				if mode == 0 {
					continue
				}
				holders := manager.granted[resource]
				if holders == nil {
					holders = map[*session]lockMode{}
					manager.granted[resource] = holders
				}
				holders[holder] = mode
				held := manager.held[holder]
				if held == nil {
					held = map[rowLockResource]lockMode{}
					manager.held[holder] = held
				}
				held[resource] = mode
			}
			if state.wait[index] == 0 {
				continue
			}
			owners := map[*session]struct{}{}
			for other := range sessions {
				if state.wait[index]&(1<<other) != 0 {
					owners[sessions[other]] = struct{}{}
				}
			}
			manager.waiting[holder] = owners
		}
		manager.mu.Unlock()
	}
	observe := func(current control) (snap, string) {
		var state snap
		state.control = current
		manager.mu.Lock()
		defer manager.mu.Unlock()
		for index, holder := range sessions {
			for row, resource := range rows {
				mode := manager.granted[resource][holder]
				state.grant[index][row] = uint8(mode)
				if manager.held[holder][resource] != mode {
					return state, "a held lock does not match the granted lock"
				}
			}
			for other, owner := range sessions {
				if _, waiting := manager.waiting[holder][owner]; waiting {
					state.wait[index] |= 1 << other
				}
			}
		}
		for _, resource := range rows {
			holders := manager.granted[resource]
			for left, leftMode := range holders {
				for right, rightMode := range holders {
					if left != right && lockModesConflict(leftMode, rightMode) {
						return state, "two sessions hold conflicting locks on row " + resource.key
					}
				}
			}
		}
		return state, ""
	}
	listed := func(mask uint8) []rowLockResource {
		var out []rowLockResource
		for index, resource := range rows {
			if mask&(1<<index) != 0 {
				out = append(out, resource)
			}
		}
		return out
	}
	ownerMask := func(session *session, resources []rowLockResource, mode lockMode) uint8 {
		var mask uint8
		owners := manager.conflictingOwners(session, resources, mode)
		for index, owner := range sessions {
			if _, found := owners[owner]; found {
				mask |= 1 << index
			}
		}
		return mask
	}
	realCycle := func(actor int, resources []rowLockResource, mode lockMode, current control) bool {
		var edges [3]uint8
		for index, session := range sessions {
			switch {
			case index == actor:
				edges[index] = ownerMask(session, resources, mode)
			case current.phase[index] == phaseWaiting:
				edges[index] = ownerMask(session, listed(current.want[index]), current.mode[index])
			}
		}
		var seen [3]bool
		var walk func(int) bool
		walk = func(index int) bool {
			if index == actor {
				return true
			}
			if seen[index] {
				return false
			}
			seen[index] = true
			for next := range sessions {
				if edges[index]&(1<<next) != 0 && walk(next) {
					return true
				}
			}
			return false
		}
		for next := range sessions {
			if edges[actor]&(1<<next) != 0 && walk(next) {
				return true
			}
		}
		return false
	}
	describe := func(action step) string {
		if action.kind == "release" {
			return names[action.session] + " releases its locks"
		}
		var locked []string
		for index := range rows {
			if action.rows&(1<<index) != 0 {
				locked = append(locked, "row "+rows[index].key)
			}
		}
		how := "a shared"
		if action.mode == lockExclusive {
			how = "an exclusive"
		}
		return fmt.Sprintf("%s %s %s lock on %s", names[action.session], action.result, how, strings.Join(locked, " and "))
	}

	var nodes []node
	trace := func(at int, action step, harm string) string {
		t.Helper()
		var lines []string
		var walk func(int)
		walk = func(index int) {
			if index < 0 {
				return
			}
			walk(nodes[index].parent)
			if nodes[index].parent >= 0 {
				lines = append(lines, describe(nodes[index].step))
			}
		}
		walk(at)
		lines = append(lines, describe(action))
		return "row locks " + harm + "\n" + strings.Join(lines, "\n")
	}

	nodes = []node{{-1, step{}, start}}
	seen := map[snap]struct{}{start: {}}
	falseDeadlock := ""
	missedCycle := ""
	for frontier := 0; frontier < len(nodes); frontier++ {
		current := nodes[frontier].state
		try := func(action step) {
			write(current)
			session := sessions[action.session]
			resources := listed(action.rows)
			cycle := false
			if action.kind != "release" {
				cycle = realCycle(action.session, resources, action.mode, current.control)
			}
			next := current.control
			switch action.kind {
			case "acquire", "retry":
				_, acquired, err := manager.waitAttempt(session, resources, action.mode)
				switch {
				case err != nil:
					if !isScheduleDeadlock(err) {
						t.Fatalf("waitAttempt error: %v", err)
					}
					action.result = "is told to roll back while waiting for"
					if !cycle && falseDeadlock == "" {
						falseDeadlock = trace(frontier, action, "reports a deadlock when no session waits for a lock another session holds now")
					}
					next.phase[action.session] = phaseDeadlock
					next.want[action.session] = action.rows
					next.mode[action.session] = action.mode
					next.justified[action.session] = true
				case acquired:
					action.result = "takes"
					next.phase[action.session] = phaseIdle
					next.justified[action.session] = true
				default:
					action.result = "waits for"
					if cycle && missedCycle == "" {
						missedCycle = trace(frontier, action, "leaves a real lock-wait cycle in place")
					}
					next.phase[action.session] = phaseWaiting
					next.want[action.session] = action.rows
					next.mode[action.session] = action.mode
					next.justified[action.session] = true
				}
			case "nowait":
				acquired, err := lockNowait(manager, session, resources, action.mode)
				if err != nil && !isNowaitRefusal(err) {
					t.Fatalf("lockNowait error: %v", err)
				}
				if acquired {
					action.result = "takes"
				} else {
					action.result = "does not take"
				}
			case "skip":
				acquired, err := manager.skipLocked(session, resources, action.mode)
				if err != nil {
					t.Fatalf("skipLocked error: %v", err)
				}
				if acquired {
					action.result = "takes"
				} else {
					action.result = "does not take"
				}
			case "release":
				manager.release(session)
				next.phase[action.session] = phaseIdle
				next.want[action.session] = 0
				next.justified[action.session] = true
			}
			state, problem := observe(next)
			if problem != "" {
				t.Fatal(trace(frontier, action, problem))
			}
			if _, found := seen[state]; found {
				return
			}
			seen[state] = struct{}{}
			nodes = append(nodes, node{frontier, action, state})
		}
		for index := range sessions {
			switch current.phase[index] {
			case phaseDeadlock:
				try(step{kind: "release", session: index})
			case phaseWaiting:
				try(step{kind: "retry", session: index, rows: current.want[index], mode: current.mode[index]})
				try(step{kind: "release", session: index})
			default:
				holding := false
				for _, mode := range current.grant[index] {
					holding = holding || mode != 0
				}
				if holding {
					try(step{kind: "release", session: index})
				}
				for mask := uint8(1); mask < 1<<len(rows); mask++ {
					for _, mode := range []lockMode{lockShared, lockExclusive} {
						try(step{kind: "acquire", session: index, rows: mask, mode: mode})
						try(step{kind: "nowait", session: index, rows: mask, mode: mode})
						try(step{kind: "skip", session: index, rows: mask, mode: mode})
					}
				}
			}
		}
		if len(seen) > 200000 {
			t.Fatalf("row lock schedule exceeded 200000 states")
		}
	}
	t.Logf("row lock schedules visited %d states", len(seen))
	if missedCycle != "" || falseDeadlock != "" {
		t.Fatalf("%s\n%s", missedCycle, falseDeadlock)
	}
}

func lockModesConflict(held, wanted lockMode) bool {
	return held != 0 && wanted != 0 && (held == lockExclusive || wanted == lockExclusive)
}

func isScheduleDeadlock(err error) bool {
	var failure sqlFailure
	return errors.As(err, &failure) && failure.code == 1213 && failure.state == "40001"
}

func isNowaitRefusal(err error) bool {
	var failure sqlFailure
	return errors.As(err, &failure) && failure.code == 3572 && failure.state == "HY000"
}
