package main

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/jonbaldie/database/internal/instance"
)

const defaultOnlineAddress = "127.0.0.1:3306"

type onlineConnectionRequest struct {
	address       string
	account       string
	passwordFile  string
	passwordStdin bool
	tlsMode       string
	tlsCAFile     string
	tlsServerName string
}

type onlineConnectionParser struct {
	request   onlineConnectionRequest
	seen      map[string]bool
	remaining []string
}

func parseOnlineConnectionRequest(args []string) (onlineConnectionRequest, []string, error) {
	parser := onlineConnectionParser{
		request: onlineConnectionRequest{address: defaultOnlineAddress, tlsMode: "disabled"},
		seen:    map[string]bool{},
	}
	argumentCount := len(args)
	for index := 0; index < argumentCount; index++ {
		nextIndex, err := parser.consume(args, index)
		if err != nil {
			return onlineConnectionRequest{}, nil, err
		}
		index = nextIndex
	}
	if err := validateOnlineConnectionRequest(parser.request); err != nil {
		return onlineConnectionRequest{}, nil, err
	}
	return parser.request, parser.remaining, nil
}

func (parser *onlineConnectionParser) consume(args []string, index int) (int, error) {
	name, value, hasValue := strings.Cut(args[index], "=")
	if name == "--password-stdin" {
		return index, parser.setPasswordStdin(hasValue)
	}
	if !isOnlineValueFlag(name) {
		parser.remaining = append(parser.remaining, args[index])
		return index, nil
	}
	value, nextIndex, err := onlineFlagValue(args, index, name, value, hasValue)
	if err != nil {
		return index, err
	}
	return nextIndex, parser.setValue(name, value)
}

func isOnlineValueFlag(name string) bool {
	switch name {
	case "--address", "--account", "--password-file", "--tls", "--tls-ca-file", "--tls-server-name":
		return true
	default:
		return false
	}
}

func (parser *onlineConnectionParser) setPasswordStdin(hasValue bool) error {
	if hasValue {
		return errors.New("--password-stdin does not take a value")
	}
	if parser.request.passwordFile != "" || parser.request.passwordStdin {
		return errors.New("password source may be specified once")
	}
	parser.request.passwordStdin = true
	return nil
}

func onlineFlagValue(args []string, index int, name, value string, hasValue bool) (string, int, error) {
	if hasValue {
		if value == "" {
			return "", index, fmt.Errorf("%s has an empty value", name)
		}
		return value, index, nil
	}
	if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
		return "", index, fmt.Errorf("%s requires a value", name)
	}
	return args[index+1], index + 1, nil
}

func (parser *onlineConnectionParser) setValue(name, value string) error {
	if parser.seen[name] {
		return fmt.Errorf("%s may be specified once", name)
	}
	parser.seen[name] = true
	switch name {
	case "--address":
		parser.request.address = value
		return nil
	case "--account":
		parser.request.account = value
		return nil
	case "--password-file":
		return parser.setPasswordFile(value)
	case "--tls":
		return parser.setTLSMode(value)
	case "--tls-ca-file":
		parser.request.tlsCAFile = value
		return nil
	case "--tls-server-name":
		parser.request.tlsServerName = value
		return nil
	default:
		return fmt.Errorf("unknown flag %q", name)
	}
}

func (parser *onlineConnectionParser) setPasswordFile(value string) error {
	if parser.request.passwordFile != "" || parser.request.passwordStdin {
		return errors.New("password source may be specified once")
	}
	parser.request.passwordFile = value
	return nil
}

func (parser *onlineConnectionParser) setTLSMode(value string) error {
	if value != "disabled" && value != "verify-full" {
		return errors.New("--tls must be disabled or verify-full")
	}
	parser.request.tlsMode = value
	return nil
}

func validateOnlineConnectionRequest(request onlineConnectionRequest) error {
	if request.account == "" {
		return errors.New("online command requires --account")
	}
	if request.passwordFile == "" && !request.passwordStdin {
		return errors.New("online command requires --password-file or --password-stdin")
	}
	if request.tlsMode == "disabled" && (request.tlsCAFile != "" || request.tlsServerName != "") {
		return errors.New("TLS trust options require --tls=verify-full")
	}
	return nil
}

func readOnlinePassword(request onlineConnectionRequest, stdin io.Reader) (string, error) {
	if request.passwordStdin {
		return instance.ReadPassword("", stdin)
	}
	return instance.ReadPassword(request.passwordFile, stdin)
}

func openOnlineDatabase(request onlineConnectionRequest, password string) (*sql.DB, error) {
	config := mysqldriver.NewConfig()
	config.User = request.account
	config.Passwd = password
	config.Net = "tcp"
	config.Addr = request.address
	config.Params = map[string]string{"allowCleartextPasswords": "false"}
	if err := applyOnlineTLS(config, request); err != nil {
		return nil, err
	}
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func applyOnlineTLS(config *mysqldriver.Config, request onlineConnectionRequest) error {
	if request.tlsMode != "verify-full" {
		return nil
	}
	tlsConfig, err := onlineTLSConfig(request)
	if err != nil {
		return err
	}
	name := "database-online-" + strings.ReplaceAll(request.address, ":", "-")
	if err := mysqldriver.RegisterTLSConfig(name, tlsConfig); err != nil {
		return err
	}
	config.TLSConfig = name
	return nil
}

func onlineTLSConfig(request onlineConnectionRequest) (*tls.Config, error) {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if err := appendOnlineCA(roots, request.tlsCAFile); err != nil {
		return nil, err
	}
	serverName, err := onlineTLSServerName(request)
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: serverName}, nil
}

func appendOnlineCA(roots *x509.CertPool, path string) error {
	if path == "" {
		return nil
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read TLS CA file: %w", err)
	}
	if !roots.AppendCertsFromPEM(pem) {
		return errors.New("TLS CA file contains no certificates")
	}
	return nil
}

func onlineTLSServerName(request onlineConnectionRequest) (string, error) {
	if request.tlsServerName != "" {
		return request.tlsServerName, nil
	}
	host, _, err := net.SplitHostPort(request.address)
	if err != nil {
		return "", fmt.Errorf("derive TLS server name: %w", err)
	}
	return host, nil
}

func onlineNonLoopbackWarning(address string) map[string]string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() || host == "localhost" {
		return nil
	}
	return map[string]string{"address": address, "tls": "disabled"}
}
