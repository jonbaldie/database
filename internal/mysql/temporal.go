// This file implements the v0.1 strict temporal value contract: the DATE, TIME,
// DATETIME, TIMESTAMP, and YEAR families, their fractional second precision, and
// the reproducible fixed-offset rendering of a TIMESTAMP instant through a
// session time zone. A zero date or year, a two-digit year, an ambiguous or
// malformed spelling, an invalid calendar value, an out-of-range value, and
// excess fractional precision all fail with a MySQL 8.4.11 error identity before
// any durable effect, so a rejected write never changes a table. v0.1 never
// silently rounds, truncates, or normalizes a temporal value.
package mysql

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type temporalKind int

const (
	temporalNone temporalKind = iota
	temporalDate
	temporalTime
	temporalDatetime
	temporalTimestamp
	temporalYear
)

// temporalType is the parsed description of a declared temporal column. A
// temporalNone kind means the declaration belongs to another contract (numeric,
// character, or an unknown legacy type).
type temporalType struct {
	kind      temporalKind
	precision int
	wire      byte
	length    uint32
}

type temporalFamily struct {
	kind         temporalKind
	wire         byte
	length       uint32
	hasPrecision bool
}

var temporalFamilies = map[string]temporalFamily{
	"DATE":      {kind: temporalDate, wire: mysqlTypeDate, length: 10, hasPrecision: false},
	"TIME":      {kind: temporalTime, wire: mysqlTypeTime, length: 10, hasPrecision: true},
	"DATETIME":  {kind: temporalDatetime, wire: mysqlTypeDatetime, length: 19, hasPrecision: true},
	"TIMESTAMP": {kind: temporalTimestamp, wire: mysqlTypeTimestamp, length: 19, hasPrecision: true},
	"YEAR":      {kind: temporalYear, wire: mysqlTypeYear, length: 4, hasPrecision: false},
}

// parseTemporalType parses a declared column type. A temporalNone kind means the
// declaration is not a temporal family; a non-nil error means the declaration
// names a fractional precision the family does not accept or a precision outside
// the zero-through-six ceiling.
func parseTemporalType(typeName string) (temporalType, error) {
	base, argument := splitTemporalType(typeName)
	family, ok := temporalFamilies[base]
	if !ok {
		return temporalType{}, nil
	}
	precision, err := temporalPrecision(family, base, argument)
	if err != nil {
		return temporalType{}, err
	}
	return temporalType{kind: family.kind, precision: precision, wire: family.wire, length: family.length}, nil
}

// splitTemporalType returns the upper-cased base name and the parenthesised
// precision argument of a declared temporal type.
func splitTemporalType(typeName string) (string, string) {
	normalized := strings.ToUpper(strings.TrimSpace(typeName))
	open := strings.IndexByte(normalized, '(')
	if open < 0 {
		return normalized, ""
	}
	end := strings.LastIndexByte(normalized, ')')
	if end <= open {
		return normalized, ""
	}
	return strings.TrimSpace(normalized[:open]), strings.TrimSpace(normalized[open+1 : end])
}

func temporalPrecision(family temporalFamily, base, argument string) (int, error) {
	if argument == "" {
		return 0, nil
	}
	if !family.hasPrecision {
		return 0, sqlFailure{1064, "42000", fmt.Sprintf("%s does not accept a fractional precision", base)}
	}
	precision, err := strconv.Atoi(argument)
	if err != nil {
		return 0, sqlFailure{1064, "42000", fmt.Sprintf("invalid %s precision", base)}
	}
	if precision < 0 || precision > 6 {
		return 0, sqlFailure{1426, "42000", fmt.Sprintf("Too-big precision %d specified for column. Maximum is 6", precision)}
	}
	return precision, nil
}

// temporalWireType maps a parsed temporal type to its MySQL result column wire
// type, display length, and the binary character set every temporal value
// advertises. A non-zero fractional precision widens the display length by the
// decimal point and its digits.
func temporalWireType(typ temporalType) (byte, uint32, uint16) {
	length := typ.length
	if typ.precision > 0 {
		length += uint32(1 + typ.precision)
	}
	return typ.wire, length, mysqlCharsetBinary
}

func temporalTypeForKind(kind temporalKind, precision int) temporalType {
	for _, family := range temporalFamilies {
		if family.kind == kind {
			return temporalType{kind: kind, precision: precision, wire: family.wire, length: family.length}
		}
	}
	return temporalType{kind: kind, precision: precision}
}

func temporalResultMetadata(name string, kind temporalKind, precision int) columnMetadata {
	typ := temporalTypeForKind(kind, precision)
	wire, length, charset := temporalWireType(typ)
	return columnMetadata{
		catalog:      "def",
		name:         name,
		characterSet: charset,
		length:       length,
		typ:          wire,
		decimals:     byte(precision),
	}
}

// canonicalTemporalValue validates a supplied literal against a temporal column
// and returns its canonical stored representation. A NULL literal passes through
// unchanged. Any zero, malformed, ambiguous, out-of-range, or excessively
// precise value is rejected with a MySQL error identity so the caller can fail
// before durability.
func canonicalTemporalValue(typ temporalType, raw, column string, row int) (string, error) {
	return canonicalTemporalValueAtOffset(typ, raw, column, row, 0)
}

// canonicalTemporalValueAtOffset validates a temporal literal and stores a
// TIMESTAMP as its UTC instant. Other temporal families remain wall-clock
// values. The offset is the session-local offset used to interpret a supplied
// TIMESTAMP literal.
func canonicalTemporalValueAtOffset(typ temporalType, raw, column string, row, offsetMinutes int) (string, error) {
	value := strings.TrimSpace(raw)
	if strings.EqualFold(value, "null") {
		return "NULL", nil
	}
	switch typ.kind {
	case temporalDate:
		return canonicalDateValue(value, column, row)
	case temporalYear:
		return canonicalYearValue(value, column, row)
	case temporalTime:
		return canonicalTimeValue(typ, value, column, row)
	case temporalDatetime:
		return canonicalDatetimeValue(typ, value, column, row, false)
	case temporalTimestamp:
		return canonicalTimestampValue(typ, value, column, row, offsetMinutes)
	default:
		return value, nil
	}
}

func canonicalDateValue(value, column string, row int) (string, error) {
	year, month, day, err := parseCalendarDate(value, column, row)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day), nil
}

// parseCalendarDate reads the canonical YYYY-MM-DD spelling and enforces the
// supported calendar. A two-digit year, an alternate separator, a zero month or
// day, an out-of-range year, and an impossible calendar date all fail.
func parseCalendarDate(value, column string, row int) (int, int, int, error) {
	comps, ok := fixedComponents(value, "-", []int{4, 2, 2})
	if !ok {
		return 0, 0, 0, incorrectTemporal("DATE", column, value, row)
	}
	year, month, day := comps[0], comps[1], comps[2]
	if year < 1000 || year > 9999 {
		return 0, 0, 0, outOfRange(column, row)
	}
	if !validCalendarDay(year, month, day) {
		return 0, 0, 0, incorrectTemporal("DATE", column, value, row)
	}
	return year, month, day, nil
}

func validCalendarDay(year, month, day int) bool {
	return month >= 1 && month <= 12 && day >= 1 && day <= daysInMonth(year, month)
}

// fixedComponents splits value on sep into exactly len(widths) numeric fields,
// each of its required character width, returning the parsed integers. The
// second result is false when the field count, a field width, or a digit run is
// malformed, which is how an ambiguous or alternately spelled value is rejected.
func fixedComponents(value, sep string, widths []int) ([]int, bool) {
	parts := strings.Split(value, sep)
	if len(parts) != len(widths) {
		return nil, false
	}
	values := make([]int, len(parts))
	for index, part := range parts {
		if len(part) != widths[index] {
			return nil, false
		}
		parsed, ok := parseFixedDigits(part)
		if !ok {
			return nil, false
		}
		values[index] = parsed
	}
	return values, true
}

func canonicalYearValue(value, column string, row int) (string, error) {
	if len(value) != 4 {
		return "", incorrectTemporal("YEAR", column, value, row)
	}
	year, ok := parseFixedDigits(value)
	if !ok {
		return "", incorrectTemporal("YEAR", column, value, row)
	}
	if year < 1901 || year > 2155 {
		return "", outOfRange(column, row)
	}
	return fmt.Sprintf("%04d", year), nil
}

func canonicalTimeValue(typ temporalType, value, column string, row int) (string, error) {
	negative := strings.HasPrefix(value, "-")
	body := strings.TrimPrefix(value, "-")
	clock, fraction, err := splitTemporalFraction(typ, body, "TIME", column, row)
	if err != nil {
		return "", err
	}
	hours, minutes, seconds, ok := parseTimeClock(clock)
	if !ok || minutes > 59 || seconds > 59 {
		return "", incorrectTemporal("TIME", column, value, row)
	}
	// Minutes and seconds are already bounded, so 838:59:59 is the inclusive
	// ceiling and only the hour field can carry a value past the range.
	if hours > 838 {
		return "", outOfRange(column, row)
	}
	sign := ""
	if negative {
		sign = "-"
	}
	return sign + fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds) + fraction, nil
}

// parseTimeClock reads an HH:MM:SS clock whose hour field may carry one to three
// digits, so a TIME value up to 838 hours is expressible while the minute and
// second fields keep their fixed two-digit width.
func parseTimeClock(clock string) (int, int, int, bool) {
	parts := strings.Split(clock, ":")
	if len(parts) != 3 || len(parts[0]) < 1 || len(parts[0]) > 3 || len(parts[1]) != 2 || len(parts[2]) != 2 {
		return 0, 0, 0, false
	}
	hours, hoursOK := parseFixedDigits(parts[0])
	minutes, minutesOK := parseFixedDigits(parts[1])
	seconds, secondsOK := parseFixedDigits(parts[2])
	if !hoursOK || !minutesOK || !secondsOK {
		return 0, 0, 0, false
	}
	return hours, minutes, seconds, true
}

func canonicalDatetimeValue(typ temporalType, value, column string, row int, timestamp bool) (string, error) {
	label := "DATETIME"
	if timestamp {
		label = "TIMESTAMP"
	}
	datePart, clockPart, found := strings.Cut(value, " ")
	if !found {
		return "", incorrectTemporal(label, column, value, row)
	}
	clock, fraction, err := splitTemporalFraction(typ, clockPart, label, column, row)
	if err != nil {
		return "", err
	}
	year, month, day, err := parseCalendarDate(datePart, column, row)
	if err != nil {
		return "", err
	}
	hours, minutes, seconds, err := parseClock(clock, label, column, value, row)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d", year, month, day, hours, minutes, seconds) + fraction, nil
}

func canonicalTimestampValue(typ temporalType, value, column string, row, offsetMinutes int) (string, error) {
	canonical, err := canonicalDatetimeValue(typ, value, column, row, true)
	if err != nil {
		return "", err
	}
	instant, err := parseCanonicalInstant(canonical, column, row)
	if err != nil {
		return "", err
	}
	stored := instant.Add(-time.Duration(offsetMinutes) * time.Minute)
	if err := enforceTimestampInstantRange(stored, column, row); err != nil {
		return "", err
	}
	return renderInstant(stored, typ.precision), nil
}

func parseCanonicalInstant(value, column string, row int) (time.Time, error) {
	datePart, clockPart, found := strings.Cut(value, " ")
	if !found {
		return time.Time{}, incorrectTemporal("TIMESTAMP", column, value, row)
	}
	clock, fraction, err := splitTemporalFraction(temporalType{precision: 6}, clockPart, "TIMESTAMP", column, row)
	if err != nil {
		return time.Time{}, err
	}
	year, month, day, err := parseCalendarDate(datePart, column, row)
	if err != nil {
		return time.Time{}, err
	}
	hours, minutes, seconds, err := parseClock(clock, "TIMESTAMP", column, value, row)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(year, time.Month(month), day, hours, minutes, seconds, temporalFractionNanoseconds(fraction), time.UTC), nil
}

func parseClock(clock, label, column, value string, row int) (int, int, int, error) {
	comps, ok := fixedComponents(clock, ":", []int{2, 2, 2})
	if !ok {
		return 0, 0, 0, incorrectTemporal(label, column, value, row)
	}
	hours, minutes, seconds := comps[0], comps[1], comps[2]
	if hours > 23 || minutes > 59 || seconds > 59 {
		return 0, 0, 0, incorrectTemporal(label, column, value, row)
	}
	return hours, minutes, seconds, nil
}

// enforceTimestampRange rejects any instant outside MySQL's supported TIMESTAMP
// window of 1970-01-01 00:00:01 through 2038-01-19 03:14:07 (UTC), the reference
// bounds a fixed-offset session renders against.
func enforceTimestampRange(year, month, day, hours, minutes, seconds int, column string, row int) error {
	instant := time.Date(year, time.Month(month), day, hours, minutes, seconds, 0, time.UTC)
	return enforceTimestampInstantRange(instant, column, row)
}

func enforceTimestampInstantRange(instant time.Time, column string, row int) error {
	floor := time.Date(1970, 1, 1, 0, 0, 1, 0, time.UTC)
	ceiling := time.Date(2038, 1, 19, 3, 14, 7, 999999999, time.UTC)
	if instant.Before(floor) || instant.After(ceiling) {
		return outOfRange(column, row)
	}
	return nil
}

// splitTemporalFraction separates a clock body from its fractional second run
// and canonicalizes the run to exactly the column's declared precision. A
// fractional run longer than the precision is excess precision and fails rather
// than rounding; a shorter run is padded with trailing zeros; a fractional part
// on a zero-precision column fails.
func splitTemporalFraction(typ temporalType, body, label, column string, row int) (string, string, error) {
	clock, frac, found := strings.Cut(body, ".")
	if !found {
		if typ.precision == 0 {
			return clock, "", nil
		}
		return clock, "." + strings.Repeat("0", typ.precision), nil
	}
	if frac == "" || !allDigits(frac) {
		return "", "", incorrectTemporal(label, column, body, row)
	}
	if len(frac) > typ.precision {
		return "", "", excessTemporalPrecision(column, body, row)
	}
	return clock, "." + frac + strings.Repeat("0", typ.precision-len(frac)), nil
}

func daysInMonth(year, month int) int {
	switch month {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if isLeapYear(year) {
			return 29
		}
		return 28
	default:
		return 0
	}
}

func isLeapYear(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// parseFixedDigits parses a run that must be non-empty ASCII digits, so a signed
// or space-padded field is rejected as a malformed temporal component.
func parseFixedDigits(run string) (int, bool) {
	if run == "" || !allDigits(run) {
		return 0, false
	}
	value, err := strconv.Atoi(run)
	if err != nil {
		return 0, false
	}
	return value, true
}

// parseFixedOffset resolves a supported fixed-offset session time-zone value to
// its signed offset in minutes. UTC and a ±HH:MM offset within ±14:00 are
// supported; a named zone, the SYSTEM zone, and a malformed or out-of-range
// offset are rejected so session rendering stays reproducible.
func parseFixedOffset(zone string) (int, error) {
	trimmed := strings.TrimSpace(zone)
	if strings.EqualFold(trimmed, "UTC") {
		return 0, nil
	}
	total, ok := parseSignedOffset(trimmed)
	if !ok || total > 14*60 || total < -14*60 {
		return 0, sqlFailure{1298, "HY000", fmt.Sprintf("Unknown or unsupported time zone: '%s'", zone)}
	}
	return total, nil
}

func formatFixedOffset(total int) string {
	sign := "+"
	if total < 0 {
		sign = "-"
		total = -total
	}
	return fmt.Sprintf("%s%02d:%02d", sign, total/60, total%60)
}

// parseSignedOffset reads a ±HH:MM offset into signed minutes. The second result
// is false when the spelling, the field widths, or the minute field is
// malformed, leaving the supported-range check to the caller.
func parseSignedOffset(trimmed string) (int, bool) {
	if len(trimmed) != 6 || (trimmed[0] != '+' && trimmed[0] != '-') || trimmed[3] != ':' {
		return 0, false
	}
	hours, hoursOK := parseFixedDigits(trimmed[1:3])
	minutes, minutesOK := parseFixedDigits(trimmed[4:6])
	if !hoursOK || !minutesOK || minutes > 59 {
		return 0, false
	}
	total := hours*60 + minutes
	if trimmed[0] == '-' {
		return -total, true
	}
	return total, true
}

// renderTimestampFixedOffset renders a TIMESTAMP instant, expressed as a
// canonical UTC datetime, into the wall-clock spelling seen through a fixed
// session offset in minutes. The rendering is a pure function of the instant and
// the offset, so the same pair always produces the same value.
func renderTimestampFixedOffset(instant string, offsetMinutes, precision int) (string, error) {
	datePart, clockPart, found := strings.Cut(strings.TrimSpace(instant), " ")
	if !found {
		return "", sqlFailure{1292, "22007", fmt.Sprintf("Incorrect datetime value: '%s'", instant)}
	}
	clockPart, fraction, err := splitTemporalFraction(temporalType{precision: 6}, clockPart, "TIMESTAMP", "timestamp", 0)
	if err != nil {
		return "", err
	}
	year, month, day, err := parseCalendarDate(datePart, "timestamp", 0)
	if err != nil {
		return "", err
	}
	hours, minutes, seconds, err := parseClock(clockPart, "TIMESTAMP", "timestamp", instant, 0)
	if err != nil {
		return "", err
	}
	base := time.Date(year, time.Month(month), day, hours, minutes, seconds, temporalFractionNanoseconds(fraction), time.UTC)
	shifted := base.Add(time.Duration(offsetMinutes) * time.Minute)
	return renderInstant(shifted, precision), nil
}

func temporalFractionNanoseconds(fraction string) int {
	if fraction == "" {
		return 0
	}
	micros, _ := strconv.Atoi(strings.TrimPrefix(fraction, "."))
	return micros * 1000
}

// currentTemporal renders a captured instant as the canonical value of a
// current-time function for the requested temporal kind. Callers capture one
// instant per statement and reuse it, so every current-time reference within a
// statement observes the same value.
func currentTemporal(instant time.Time, kind temporalKind, precision int) string {
	utc := instant.UTC()
	switch kind {
	case temporalDate:
		return utc.Format("2006-01-02")
	case temporalTime:
		return renderClock(utc, precision)
	default:
		return renderInstant(utc, precision)
	}
}

func renderInstant(instant time.Time, precision int) string {
	rendered := instant.Format("2006-01-02 15:04:05")
	return rendered + fractionalSuffix(instant, precision)
}

func renderClock(instant time.Time, precision int) string {
	return instant.Format("15:04:05") + fractionalSuffix(instant, precision)
}

func fractionalSuffix(instant time.Time, precision int) string {
	if precision <= 0 {
		return ""
	}
	nanos := fmt.Sprintf("%09d", instant.Nanosecond())
	return "." + nanos[:precision]
}

// readPreparedTemporal decodes a binary-protocol temporal parameter into the
// canonical text spelling its text-protocol form would carry, so that a prepared
// and a text temporal input converge on the same stored value and metadata when
// the conversion is exact. The declared wire type selects the layout: DATE,
// DATETIME, and TIMESTAMP share a length-prefixed date, time, and microsecond
// encoding, while TIME carries a sign, a day count, and a clock. The decoded
// value is quoted so the caller unquotes and validates it through the same
// contract a text literal follows.
func readPreparedTemporal(payload []byte, offset int, typ preparedParameterType) (string, int, error) {
	if offset >= len(payload) {
		return "", offset, errors.New("malformed temporal prepared parameter")
	}
	end := offset + 1 + int(payload[offset])
	if end > len(payload) {
		return "", offset, errors.New("malformed temporal prepared parameter")
	}
	body := payload[offset+1 : end]
	if !validPreparedTemporalBodyLength(typ.typ, len(body)) {
		return "", offset, errors.New("malformed temporal prepared parameter")
	}
	if typ.typ == mysqlTypeTime {
		value, err := decodePreparedTime(body)
		if err != nil {
			return "", offset, err
		}
		return quote(value), end, nil
	}
	value, err := decodePreparedDatetime(typ.typ, body)
	if err != nil {
		return "", offset, err
	}
	return quote(value), end, nil
}

var preparedTemporalBodyLengths = map[byte]map[int]bool{
	mysqlTypeDate:      {0: true, 4: true},
	mysqlTypeDatetime:  {0: true, 4: true, 7: true, 11: true},
	mysqlTypeTimestamp: {0: true, 4: true, 7: true, 11: true},
	mysqlTypeTime:      {0: true, 8: true, 12: true},
}

func validPreparedTemporalBodyLength(wire byte, length int) bool {
	lengths, ok := preparedTemporalBodyLengths[wire]
	return ok && lengths[length]
}

func decodePreparedDatetime(wire byte, body []byte) (string, error) {
	year, month, day, hour, minute, second, micros := 0, 0, 0, 0, 0, 0, 0
	if len(body) >= 4 {
		year = int(binary.LittleEndian.Uint16(body[0:2]))
		month = int(body[2])
		day = int(body[3])
	}
	if len(body) >= 7 {
		hour = int(body[4])
		minute = int(body[5])
		second = int(body[6])
	}
	if len(body) >= 11 {
		micros = int(binary.LittleEndian.Uint32(body[7:11]))
	}
	if month > 12 || day > 31 || hour > 23 || minute > 59 || second > 59 || micros > 999999 {
		return "", errors.New("malformed temporal prepared parameter")
	}
	date := fmt.Sprintf("%04d-%02d-%02d", year, month, day)
	if wire == mysqlTypeDate {
		return date, nil
	}
	return date + fmt.Sprintf(" %02d:%02d:%02d", hour, minute, second) + preparedFraction(micros), nil
}

func decodePreparedTime(body []byte) (string, error) {
	negative, days, hour, minute, second, micros := false, 0, 0, 0, 0, 0
	if len(body) >= 8 {
		if body[0] > 1 {
			return "", errors.New("malformed temporal prepared parameter")
		}
		negative = body[0] == 1
		days = int(binary.LittleEndian.Uint32(body[1:5]))
		hour = int(body[5])
		minute = int(body[6])
		second = int(body[7])
	}
	if len(body) >= 12 {
		micros = int(binary.LittleEndian.Uint32(body[8:12]))
	}
	if hour > 23 || minute > 59 || second > 59 || micros > 999999 {
		return "", errors.New("malformed temporal prepared parameter")
	}
	totalHours := int64(days)*24 + int64(hour)
	if totalHours > 838 {
		return "", errors.New("temporal prepared parameter is out of range")
	}
	sign := ""
	if negative {
		sign = "-"
	}
	return sign + fmt.Sprintf("%02d:%02d:%02d", totalHours, minute, second) + preparedFraction(micros), nil
}

// preparedFraction renders a microsecond field as the fractional run a text
// literal would carry, dropping trailing zeros so an exact prepared value and
// its text spelling agree on fractional precision.
func preparedFraction(micros int) string {
	if micros <= 0 {
		return ""
	}
	return "." + strings.TrimRight(fmt.Sprintf("%06d", micros), "0")
}

const preparedTemporalMarkerPrefix = "\x00database-prepared-temporal:"

func preparedTemporalLiteral(wire byte, value string) string {
	return quote(preparedTemporalMarkerPrefix + strconv.Itoa(int(wire)) + ":" + value)
}

func decodePreparedTemporalLiteral(value string) (byte, string, bool) {
	if !strings.HasPrefix(value, preparedTemporalMarkerPrefix) {
		return 0, value, false
	}
	remainder := strings.TrimPrefix(value, preparedTemporalMarkerPrefix)
	separator := strings.IndexByte(remainder, ':')
	if separator <= 0 {
		return 0, value, false
	}
	wire, err := strconv.Atoi(remainder[:separator])
	if err != nil || wire < 0 || wire > 255 {
		return 0, value, false
	}
	return byte(wire), remainder[separator+1:], true
}

func incorrectTemporal(kind, column, value string, row int) error {
	return sqlFailure{1292, "22007", fmt.Sprintf("Incorrect %s value: '%s' for column '%s' at row %d", kind, value, column, row)}
}

func excessTemporalPrecision(column, value string, row int) error {
	return sqlFailure{1292, "22007", fmt.Sprintf("Excess fractional precision in temporal value '%s' for column '%s' at row %d", value, column, row)}
}
