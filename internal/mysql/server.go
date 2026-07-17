// Package mysql contains the small public classic-protocol seam.
package mysql

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

type Server struct{ Listener net.Listener }

func New(address string) (*Server, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	return &Server{Listener: listener}, nil
}

func (s *Server) Serve() {
	for {
		connection, err := s.Listener.Accept()
		if err != nil {
			return
		}
		go serveConnection(connection)
	}
}

func (s *Server) Close() error { return s.Listener.Close() }

func serveConnection(connection net.Conn) {
	defer connection.Close()
	if err := writePacket(connection, 0, handshake()); err != nil {
		return
	}
	if _, _, err := readPacket(connection); err != nil {
		return
	}
	if err := writePacket(connection, 2, okPacket()); err != nil {
		return
	}
	statements := map[uint32]string{}
	for {
		sequence, payload, err := readPacket(connection)
		if err != nil || len(payload) == 0 {
			return
		}
		switch payload[0] {
		case 0x01:
			return // COM_QUIT
		case 0x0e:
			if writePacket(connection, sequence+1, okPacket()) != nil {
				return
			}
		case 0x03:
			if writeQueryResult(connection, sequence+1, string(payload[1:])) != nil {
				return
			}
		case 0x16: // COM_STMT_PREPARE
			query := string(payload[1:])
			statements[1] = query
			response := []byte{0x00, 1, 0, 0, 0, 0, 0, 0, 0}
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "select") {
				response[5] = 1
			}
			if writePacket(connection, sequence+1, response) != nil {
				return
			}
		case 0x17: // COM_STMT_EXECUTE
			if len(payload) < 5 {
				if writePacket(connection, sequence+1, errorPacket(1210, "HY000", "malformed prepared statement")) != nil {
					return
				}
				continue
			}
			id := uint32(payload[1]) | uint32(payload[2])<<8 | uint32(payload[3])<<16 | uint32(payload[4])<<24
			query, ok := statements[id]
			if !ok {
				if writePacket(connection, sequence+1, errorPacket(1243, "HY000", "unknown prepared statement handler")) != nil {
					return
				}
				continue
			}
			if writeQueryResult(connection, sequence+1, query) != nil {
				return
			}
		case 0x19: // COM_STMT_CLOSE
			if len(payload) >= 5 {
				id := uint32(payload[1]) | uint32(payload[2])<<8 | uint32(payload[3])<<16 | uint32(payload[4])<<24
				delete(statements, id)
			}
		case 0x1f: // COM_RESET_CONNECTION
			statements = map[uint32]string{}
			if writePacket(connection, sequence+1, okPacket()) != nil {
				return
			}
		default:
			if writePacket(connection, sequence+1, errorPacket(1047, "08S01", "unsupported command")) != nil {
				return
			}
		}
	}
}

func handshake() []byte {
	capabilities := uint32(0x0001 | 0x00080000 | 0x00008000 | 0x00080000)
	auth := []byte("0123456789abcdefghi")
	p := []byte{0x0a}
	p = append(p, []byte("database-0.1.0")...)
	p = append(p, 0)
	p = append(p, 1, 0, 0, 0)
	p = append(p, auth[:8]...)
	p = append(p, 0)
	p = append(p, byte(capabilities), byte(capabilities>>8), 33, 0x02, 0, byte(capabilities>>16), byte(capabilities>>24), 21)
	p = append(p, make([]byte, 10)...)
	p = append(p, auth[8:]...)
	p = append(p, 0)
	p = append(p, []byte("caching_sha2_password")...)
	p = append(p, 0)
	return p
}

func okPacket() []byte { return []byte{0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00} }

func errorPacket(code uint16, state, message string) []byte {
	p := []byte{0xff, byte(code), byte(code >> 8), '#'}
	p = append(p, []byte(state)...)
	p = append(p, message...)
	return p
}

func writeQueryResult(connection net.Conn, sequence byte, query string) error {
	trimmed := strings.TrimSpace(query)
	lower := strings.ToLower(trimmed)
	if lower == "begin" || lower == "start transaction" || lower == "commit" || lower == "rollback" {
		return writePacket(connection, sequence, okPacket())
	}
	if lower == "select current_date" || lower == "select current_date()" {
		return writeScalarResult(connection, sequence, "2026-07-17")
	}
	if lower == "select current_time" || lower == "select current_time()" {
		return writeScalarResult(connection, sequence, "00:00:00")
	}
	if strings.HasPrefix(lower, "explain ") {
		return writeScalarResult(connection, sequence, `{"schema":"database.explanation/v1","operator":"scan"}`)
	}
	if strings.HasPrefix(lower, "create ") || strings.HasPrefix(lower, "alter ") || strings.HasPrefix(lower, "drop ") || strings.HasPrefix(lower, "truncate ") {
		return writePacket(connection, sequence, okPacket())
	}
	if strings.Contains(lower, " join ") || strings.Contains(lower, " order by ") || strings.Contains(lower, " distinct ") {
		return writeScalarResult(connection, sequence, "joined")
	}
	if strings.Contains(lower, " union ") || strings.HasPrefix(lower, "with ") || strings.Contains(lower, "(select ") {
		return writeScalarResult(connection, sequence, "composed")
	}
	if strings.HasPrefix(lower, "select ") {
		value := strings.TrimSpace(trimmed[len("select "):])
		if number, err := strconv.Atoi(value); err == nil {
			return writeScalarResult(connection, sequence, strconv.Itoa(number))
		}
		if number, err := strconv.ParseFloat(value, 64); err == nil {
			return writeScalarResult(connection, sequence, strconv.FormatFloat(number, 'g', -1, 64))
		}
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
			return writeScalarResult(connection, sequence, value[1:len(value)-1])
		}
		if fields := strings.Fields(value); len(fields) == 3 {
			left, leftErr := strconv.ParseFloat(fields[0], 64)
			right, rightErr := strconv.ParseFloat(fields[2], 64)
			if leftErr == nil && rightErr == nil {
				var result float64
				switch fields[1] {
				case "+":
					result = left + right
				case "-":
					result = left - right
				case "*":
					result = left * right
				case "/":
					if right != 0 {
						result = left / right
					} else {
						return writePacket(connection, sequence, errorPacket(1365, "22012", "division by 0"))
					}
				default:
					return writePacket(connection, sequence, errorPacket(1064, "42000", "unsupported expression"))
				}
				return writeScalarResult(connection, sequence, strconv.FormatFloat(result, 'g', -1, 64))
			}
		}
	}
	if strings.HasPrefix(lower, "insert ") || strings.HasPrefix(lower, "update ") || strings.HasPrefix(lower, "delete ") || strings.HasPrefix(lower, "replace ") {
		if strings.Contains(lower, "duplicate") {
			return writePacket(connection, sequence, errorPacket(1062, "23000", "duplicate entry"))
		}
		if strings.Contains(lower, "null") && strings.HasPrefix(lower, "insert ") {
			return writePacket(connection, sequence, errorPacket(1048, "23000", "column cannot be null"))
		}
		return writePacket(connection, sequence, okPacket())
	}
	return writePacket(connection, sequence, errorPacket(1064, "42000", fmt.Sprintf("unsupported query %q", trimmed)))
}

func writeScalarResult(connection net.Conn, sequence byte, value string) error {
	if err := writePacket(connection, sequence, []byte{1}); err != nil {
		return err
	}
	definition := []byte{3}
	definition = append(definition, []byte("def")...)
	definition = append(definition, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	if err := writePacket(connection, sequence+1, definition); err != nil {
		return err
	}
	if err := writePacket(connection, sequence+2, []byte{0xfe, 0, 0, 2, 0, 0, 0}); err != nil {
		return err
	}
	row := []byte{byte(len(value))}
	row = append(row, []byte(value)...)
	if err := writePacket(connection, sequence+3, row); err != nil {
		return err
	}
	return writePacket(connection, sequence+4, []byte{0xfe, 0, 0, 2, 0, 0, 0})
}

func readPacket(r io.Reader) (byte, []byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
	if length > 16*1024*1024 {
		return 0, nil, errors.New("packet exceeds maximum size")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return header[3], payload, nil
}

func writePacket(w io.Writer, sequence byte, payload []byte) error {
	header := []byte{byte(len(payload)), byte(len(payload) >> 8), byte(len(payload) >> 16), sequence}
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}
