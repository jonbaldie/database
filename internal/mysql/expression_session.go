package mysql

import (
	"strings"
	"time"
)

func sessionFunctionValue(s *session, name string, arguments []exprValue) (exprValue, bool, error) {
	if s == nil {
		return exprValue{}, false, nil
	}
	switch strings.ToUpper(name) {
	case "NOW", "CURRENT_TIMESTAMP", "LOCALTIME", "LOCALTIMESTAMP":
		value, err := sessionCurrentTime(s, temporalDatetime, name, arguments)
		return value, true, err
	case "CURDATE", "CURRENT_DATE":
		value, err := sessionCurrentTime(s, temporalDate, name, arguments)
		return value, true, err
	case "CURTIME", "CURRENT_TIME":
		value, err := sessionCurrentTime(s, temporalTime, name, arguments)
		return value, true, err
	case "VERSION":
		if len(arguments) != 0 {
			return exprValue{}, true, wrongArgumentCount(name)
		}
		return stringValue(sessionVersionComment(s)), true, nil
	case "DATABASE", "SCHEMA":
		if len(arguments) != 0 {
			return exprValue{}, true, wrongArgumentCount(name)
		}
		return stringValue(s.database), true, nil
	default:
		return exprValue{}, false, nil
	}
}

func sessionVersionComment(s *session) string {
	return "8.4.11-database-" + s.server.config.Version
}

func sessionCurrentTime(s *session, kind temporalKind, name string, arguments []exprValue) (exprValue, error) {
	precision, err := currentTimePrecision(name, kind, arguments)
	if err != nil {
		return exprValue{}, err
	}
	value, err := renderSessionCurrentTime(s, kind, precision)
	if err != nil {
		return exprValue{}, err
	}
	return stringValue(value), nil
}

func currentTimePrecision(name string, kind temporalKind, arguments []exprValue) (int, error) {
	if len(arguments) == 0 {
		return 0, nil
	}
	if kind == temporalDate || len(arguments) != 1 {
		return 0, wrongArgumentCount(name)
	}
	if arguments[0].isNull() {
		return 0, sqlFailure{1064, "42000", "invalid current-time precision"}
	}
	value, err := expressionInteger(arguments[0])
	if err != nil || value < 0 || value > 6 {
		return 0, sqlFailure{1064, "42000", "invalid current-time precision"}
	}
	return int(value), nil
}

func renderSessionCurrentTime(s *session, kind temporalKind, precision int) (string, error) {
	offset, err := sessionTimeZoneOffset(s)
	if err != nil {
		return "", err
	}
	instant := s.server.config.Clock().UTC()
	local := instant.Add(time.Duration(offset) * time.Minute)
	return currentTemporal(local, kind, precision), nil
}
