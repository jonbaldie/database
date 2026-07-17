package mysql

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"net"
)

type handshakeResponse struct {
	accountName string
	database    string
	token       []byte
}

// authenticationExchange owns packet sequence state across the optional TLS
// upgrade and the mandatory full caching_sha2_password exchange.
type authenticationExchange struct {
	authenticator authenticator
	connection    net.Conn
	nextSequence  byte
	secure        bool
}

func (a authenticator) authenticate(connection net.Conn, nonce []byte) (authenticationResult, error) {
	exchange := authenticationExchange{authenticator: a, connection: connection, nextSequence: 2}
	response, err := exchange.handshakeResponse()
	if err != nil {
		return exchange.failure(err)
	}
	if err := a.validate(response, nonce); err != nil {
		return exchange.failure(err)
	}
	password, err := exchange.fullPassword(nonce)
	if err != nil {
		return exchange.failure(err)
	}
	if !validPlainPassword(password, a.config.PasswordHash) {
		return exchange.failure(sqlFailure{1045, "28000", "access denied"})
	}
	return exchange.success(response), nil
}

func (e *authenticationExchange) handshakeResponse() (handshakeResponse, error) {
	payload, err := e.readInitialResponse()
	if err != nil {
		return handshakeResponse{}, err
	}
	return parseHandshakeResponse(payload, e.authenticator.tlsConfig != nil)
}

func (e *authenticationExchange) readInitialResponse() ([]byte, error) {
	sequence, payload, err := readPacket(e.connection, e.authenticator.config.MaxAllowedPacket)
	if err != nil {
		return nil, err
	}
	e.nextSequence = sequence + 1
	if !isTLSRequest(payload) {
		return payload, nil
	}
	return e.upgradeTLS()
}

func isTLSRequest(payload []byte) bool {
	return len(payload) == 32 && binary.LittleEndian.Uint32(payload[:4])&clientSSL != 0
}

func (e *authenticationExchange) upgradeTLS() ([]byte, error) {
	if e.authenticator.tlsConfig == nil {
		return nil, sqlFailure{1043, "08S01", "TLS capability is not supported"}
	}
	connection := tls.Server(e.connection, e.authenticator.tlsConfig)
	if err := connection.Handshake(); err != nil {
		return nil, err
	}
	e.connection, e.secure, e.nextSequence = connection, true, 3
	sequence, payload, err := readPacket(e.connection, e.authenticator.config.MaxAllowedPacket)
	if err != nil {
		return nil, err
	}
	e.nextSequence = sequence + 1
	return payload, nil
}

func parseHandshakeResponse(payload []byte, tlsEnabled bool) (handshakeResponse, error) {
	capabilities, err := handshakeCapabilities(payload, tlsEnabled)
	if err != nil {
		return handshakeResponse{}, err
	}
	accountName, token, offset, err := handshakeCredentials(payload, capabilities)
	if err != nil {
		return handshakeResponse{}, err
	}
	database, offset := handshakeDatabase(payload, capabilities, offset)
	if err := requireCachingSHA2Plugin(payload, offset); err != nil {
		return handshakeResponse{}, err
	}
	return handshakeResponse{accountName: accountName, database: database, token: token}, nil
}

func handshakeCapabilities(payload []byte, tlsEnabled bool) (uint32, error) {
	if len(payload) < 32 {
		return 0, sqlFailure{1043, "08S01", "malformed handshake response"}
	}
	capabilities := binary.LittleEndian.Uint32(payload[:4])
	if capabilities&^acceptedClientCapabilities(tlsEnabled) != 0 {
		return 0, sqlFailure{1043, "08S01", "unsupported client capabilities"}
	}
	if capabilities&clientProtocol41 == 0 || capabilities&clientSecureConnection == 0 || capabilities&clientPluginAuth == 0 {
		return 0, sqlFailure{1043, "08S01", "required protocol capabilities are missing"}
	}
	return capabilities, nil
}

func handshakeCredentials(payload []byte, capabilities uint32) (string, []byte, int, error) {
	accountName, offset, ok := readNullString(payload, 32)
	if !ok {
		return "", nil, 0, sqlFailure{1043, "08S01", "malformed username"}
	}
	token, offset, err := handshakeToken(payload, capabilities, offset)
	if err != nil {
		return "", nil, 0, err
	}
	return accountName, token, offset, nil
}

func handshakeToken(payload []byte, capabilities uint32, offset int) ([]byte, int, error) {
	if capabilities&clientPluginLenencData != 0 {
		token, next, ok := readLengthEncoded(payload, offset)
		if !ok {
			return nil, 0, sqlFailure{1043, "08S01", "malformed authentication response"}
		}
		return token, next, nil
	}
	if offset >= len(payload) {
		return nil, 0, sqlFailure{1043, "08S01", "missing authentication response"}
	}
	length := int(payload[offset])
	offset++
	if offset+length > len(payload) {
		return nil, 0, sqlFailure{1043, "08S01", "malformed authentication response"}
	}
	return payload[offset : offset+length], offset + length, nil
}

func handshakeDatabase(payload []byte, capabilities uint32, offset int) (string, int) {
	if capabilities&clientConnectWithDB == 0 {
		return "", offset
	}
	database, next, found := readNullString(payload, offset)
	if !found {
		return "", offset
	}
	return database, next
}

func requireCachingSHA2Plugin(payload []byte, offset int) error {
	plugin, _, ok := readNullString(payload, offset)
	if !ok || plugin != "caching_sha2_password" {
		return sqlFailure{1251, "08004", "client does not support caching_sha2_password"}
	}
	return nil
}

func (a authenticator) validate(response handshakeResponse, nonce []byte) error {
	if a.config.Username != "" && response.accountName != a.config.Username {
		return sqlFailure{1045, "28000", "access denied"}
	}
	if a.config.PasswordHash != "" && !validPasswordToken(response.token, nonce, a.config.PasswordHash) {
		return sqlFailure{1045, "28000", "access denied"}
	}
	if response.database != "" {
		return a.databaseExists(response.database)
	}
	return nil
}

func (e *authenticationExchange) fullPassword(nonce []byte) ([]byte, error) {
	if err := writePacket(e.connection, e.nextSequence, []byte{0x01, 0x04}); err != nil {
		return nil, err
	}
	e.nextSequence++
	sequence, response, err := readPacket(e.connection, e.authenticator.config.MaxAllowedPacket)
	if err != nil {
		return nil, err
	}
	e.nextSequence = sequence + 1
	if e.secure {
		return trimPassword(response), nil
	}
	return e.rsaPassword(response, nonce)
}

func (e *authenticationExchange) rsaPassword(response, nonce []byte) ([]byte, error) {
	if len(response) != 1 || response[0] != 0x02 {
		return nil, sqlFailure{1045, "28000", "secure authentication exchange required"}
	}
	if err := writePacket(e.connection, e.nextSequence, publicKeyPacket(e.authenticator.rsaKey)); err != nil {
		return nil, err
	}
	e.nextSequence++
	sequence, encrypted, err := readPacket(e.connection, e.authenticator.config.MaxAllowedPacket)
	if err != nil {
		return nil, err
	}
	e.nextSequence = sequence + 1
	password, err := decryptPassword(e.authenticator.rsaKey, encrypted, nonce)
	if err != nil {
		return nil, sqlFailure{1045, "28000", "access denied"}
	}
	return trimPassword(password), nil
}

func trimPassword(password []byte) []byte {
	return bytes.TrimSuffix(password, []byte{0})
}

func (e authenticationExchange) failure(err error) (authenticationResult, error) {
	return authenticationResult{connection: e.connection, nextSequence: e.nextSequence}, err
}

func (e authenticationExchange) success(response handshakeResponse) authenticationResult {
	return authenticationResult{connection: e.connection, accountName: response.accountName, database: response.database, nextSequence: e.nextSequence}
}
