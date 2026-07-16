// Package mysql contains the small public classic-protocol seam.
package mysql

import (
	"errors"
	"fmt"
	"io"
	"net"
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
	if strings.EqualFold(strings.TrimSpace(query), "select 1") {
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
		if err := writePacket(connection, sequence+3, []byte{1, '1'}); err != nil {
			return err
		}
		return writePacket(connection, sequence+4, []byte{0xfe, 0, 0, 2, 0, 0, 0})
	}
	return writePacket(connection, sequence, errorPacket(1064, "42000", fmt.Sprintf("unsupported query %q", strings.TrimSpace(query))))
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
