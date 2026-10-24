package mysql

import (
	"fmt"
	"strconv"

	"github.com/jonbaldie/database/internal/catalog"
)

type autoIncrementState struct {
	next      uint64
	exhausted bool
}

func autoIncrementColumns(table catalog.Table) []int {
	columns := make([]int, 0, 1)
	for index := range table.Columns {
		if catalog.ColumnAttributeAt(table, index).AutoIncrement {
			columns = append(columns, index)
		}
	}
	return columns
}

func autoIncrementColumn(table catalog.Table) (int, bool) {
	columns := autoIncrementColumns(table)
	if len(columns) == 0 {
		return 0, false
	}
	return columns[0], true
}

func autoIncrementStateForTable(table catalog.Table) autoIncrementState {
	if _, ok := autoIncrementColumn(table); !ok {
		return autoIncrementState{}
	}
	if table.AutoIncrement == 0 && !table.AutoIncrementExhausted {
		return autoIncrementState{next: 1}
	}
	return autoIncrementState{next: table.AutoIncrement, exhausted: table.AutoIncrementExhausted}
}

func setAutoIncrementState(table *catalog.Table, state autoIncrementState) {
	if _, ok := autoIncrementColumn(*table); !ok {
		table.AutoIncrement = 0
		table.AutoIncrementExhausted = false
		return
	}
	table.AutoIncrement = state.next
	table.AutoIncrementExhausted = state.exhausted
}

func resetAutoIncrementState(table *catalog.Table) {
	if _, ok := autoIncrementColumn(*table); !ok {
		return
	}
	setAutoIncrementState(table, autoIncrementState{next: 1})
}

func validateAutoIncrement(table catalog.Table) error {
	columns := autoIncrementColumns(table)
	switch len(columns) {
	case 0:
		return nil
	case 1:
		return validateAutoIncrementColumn(table, columns[0])
	default:
		return autoIncrementKeyFailure()
	}
}

func validateAutoIncrementColumn(table catalog.Table, column int) error {
	if _, err := autoIncrementType(table, column); err != nil {
		return err
	}
	columnKey := catalog.Key(table.Columns[column])
	for _, index := range effectiveTableIndexes(table) {
		if autoIncrementColumnIsIndexed(index, columnKey) {
			return nil
		}
	}
	return autoIncrementKeyFailure()
}

func autoIncrementColumnIsIndexed(index catalog.Index, columnKey string) bool {
	for _, part := range index.Parts {
		if part.Column != "" && catalog.Key(part.Column) == columnKey {
			return true
		}
	}
	return false
}

func autoIncrementType(table catalog.Table, column int) (numericType, error) {
	typeName, known := table.ColumnType(column)
	if !known {
		return numericType{}, incorrectAutoIncrementColumn(table.Columns[column])
	}
	numeric, err := parseNumericType(typeName)
	if err != nil {
		return numericType{}, err
	}
	if numeric.kind != numericInteger {
		return numericType{}, incorrectAutoIncrementColumn(table.Columns[column])
	}
	return numeric, nil
}

func autoIncrementKeyFailure() error {
	return sqlFailure{1075, "42000", "Incorrect table definition; there can be only one auto column and it must be defined as a key"}
}

func incorrectAutoIncrementColumn(column string) error {
	return sqlFailure{1063, "42000", "Incorrect column specifier for column '" + column + "'"}
}

func initializeAutoIncrementState(table *catalog.Table) error {
	if err := validateAutoIncrement(*table); err != nil {
		return err
	}
	column, ok := autoIncrementColumn(*table)
	if !ok {
		setAutoIncrementState(table, autoIncrementState{})
		return nil
	}
	if table.AutoIncrement != 0 || table.AutoIncrementExhausted {
		return nil
	}
	state := autoIncrementState{next: 1}
	limit, err := autoIncrementLimit(*table, column)
	if err != nil {
		return err
	}
	for _, row := range table.Rows {
		if column >= len(row) {
			continue
		}
		value, positive := autoIncrementPositiveValue(row[column])
		if positive {
			state = advanceAutoIncrement(state, value, limit)
		}
	}
	setAutoIncrementState(table, state)
	return nil
}

func autoIncrementLimit(table catalog.Table, column int) (uint64, error) {
	numeric, err := autoIncrementType(table, column)
	if err != nil {
		return 0, err
	}
	if numeric.unsigned {
		return numeric.umax, nil
	}
	return uint64(numeric.smax), nil
}

// fillAutoIncrementValue assigns the next AUTO_INCREMENT value when the
// column is NULL. The boolean is true only when this call generated a value.
func fillAutoIncrementValue(table catalog.Table, row []string, state autoIncrementState, rowNumber int) (autoIncrementState, bool, error) {
	column, ok := autoIncrementColumn(table)
	if !ok {
		return state, false, nil
	}
	limit, err := autoIncrementLimit(table, column)
	if err != nil {
		return state, false, err
	}
	if row[column] == storedSQLNullValue {
		if state.exhausted || state.next == 0 || state.next > limit {
			return state, false, autoIncrementOverflow(table.Columns[column], rowNumber)
		}
		row[column] = strconv.FormatUint(state.next, 10)
		if state.next == limit {
			state.exhausted = true
		} else {
			state.next++
		}
		return state, true, nil
	}
	value, positive := autoIncrementPositiveValue(row[column])
	if positive {
		state = advanceAutoIncrement(state, value, limit)
	}
	return state, false, nil
}

// statementInsertID is the OK-packet last insert ID for one INSERT or REPLACE.
// A generated value that the statement stores wins. Otherwise the packet
// carries the AUTO_INCREMENT value of the last row that the statement stored
// or changed.
type statementInsertID struct {
	firstGenerated uint64
	hasGenerated   bool
	lastStored     uint64
	hasStored      bool
}

func (id *statementInsertID) record(table catalog.Table, row []string, generated bool) {
	column, ok := autoIncrementColumn(table)
	if !ok || column >= len(row) {
		return
	}
	value, ok := autoIncrementProtocolID(row[column])
	if !ok {
		return
	}
	if generated && !id.hasGenerated {
		id.firstGenerated = value
		id.hasGenerated = true
	}
	id.lastStored = value
	id.hasStored = true
}

func (id statementInsertID) value() uint64 {
	if id.hasGenerated {
		return id.firstGenerated
	}
	if id.hasStored {
		return id.lastStored
	}
	return 0
}

func autoIncrementProtocolID(value string) (uint64, bool) {
	if value == "" || value == storedSQLNullValue {
		return 0, false
	}
	if parsed, err := strconv.ParseUint(value, 10, 64); err == nil {
		return parsed, true
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return uint64(parsed), true
}

func autoIncrementAdvanced(before, after autoIncrementState) bool {
	if after.exhausted && !before.exhausted {
		return true
	}
	return after.next > before.next
}

func fartherAutoIncrementState(current, allocated autoIncrementState) autoIncrementState {
	if allocated.exhausted && !current.exhausted {
		return allocated
	}
	if current.exhausted || current.next >= allocated.next {
		return current
	}
	return allocated
}

func advanceAutoIncrement(state autoIncrementState, value, limit uint64) autoIncrementState {
	if state.exhausted || value < state.next {
		return state
	}
	if value >= limit {
		state.next = limit
		state.exhausted = true
		return state
	}
	state.next = value + 1
	return state
}

func ratchetAutoIncrementForUpdatedRow(table catalog.Table, previous, current []string, state autoIncrementState) (autoIncrementState, error) {
	column, ok := autoIncrementColumn(table)
	if !ok || column >= len(previous) || column >= len(current) || previous[column] == current[column] {
		return state, nil
	}
	value, positive := autoIncrementPositiveValue(current[column])
	if !positive {
		return state, nil
	}
	limit, err := autoIncrementLimit(table, column)
	if err != nil {
		return state, err
	}
	return advanceAutoIncrement(state, value, limit), nil
}

func autoIncrementPositiveValue(value string) (uint64, bool) {
	if value == storedSQLNullValue {
		return 0, false
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	return parsed, err == nil && parsed > 0
}

func autoIncrementOverflow(column string, row int) error {
	return sqlFailure{1467, "HY000", fmt.Sprintf("Failed to read auto-increment value for column '%s' at row %d", column, row)}
}
