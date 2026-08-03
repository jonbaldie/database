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
	addressSeen   bool
	accountSeen   bool
	tlsSeen       bool
	tlsCASeen     bool
	tlsServerSeen bool
}

func parseOnlineConnectionRequest(args []string) (onlineConnectionRequest, []string, error) {
	request := onlineConnectionRequest{address: defaultOnlineAddress, tlsMode: "disabled"}
	remaining := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		name, value, hasValue := strings.Cut(args[index], "=")
		if isOnlineBoolean(name) {
			if err := setOnlineBoolean(&request, name, hasValue); err != nil {
				return onlineConnectionRequest{}, nil, err
			}
			continue
		}
		if !isOnlineValueFlag(name) {
			remaining = append(remaining, args[index])
			continue
		}
		value, nextIndex, err := onlineFlagValue(args, index, name, value, hasValue)
		if err != nil {
			return onlineConnectionRequest{}, nil, err
		}
		if err := setOnlineValue(&request, name, value); err != nil {
			return onlineConnectionRequest{}, nil, err
		}
		index = nextIndex
	}
	if err := validateOnlineConnectionRequest(request); err != nil {
		return onlineConnectionRequest{}, nil, err
	}
	return request, remaining, nil
}

func isOnlineBoolean(name string) bool {
	return name == "--password-stdin"
}

func isOnlineValueFlag(name string) bool {
	switch name {
	case "--address", "--account", "--password-file", "--tls", "--tls-ca-file", "--tls-server-name":
		return true
	default:
		return false
	}
}

func setOnlineBoolean(request *onlineConnectionRequest, name string, hasValue bool) error {
	if hasValue {
		return fmt.Errorf("%s does not take a value", name)
	}
	if name != "--password-stdin" {
		return fmt.Errorf("unknown flag %q", name)
	}
	if request.passwordFile != "" || request.passwordStdin {
		return errors.New("password source may be specified once")
	}
	request.passwordStdin = true
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

func setOnlineValue(request *onlineConnectionRequest, name, value string) error {
	switch name {
	case "--address":
		if request.addressSeen {
			return errors.New("--address may be specified once")
		}
		request.addressSeen = true
		request.address = value
	case "--account":
		if request.accountSeen {
			return errors.New("--account may be specified once")
		}
		request.accountSeen = true
		request.account = value
	case "--password-file":
		if request.passwordFile != "" || request.passwordStdin {
			return errors.New("password source may be specified once")
		}
		request.passwordFile = value
	case "--tls":
		if request.tlsSeen {
			return errors.New("--tls may be specified once")
		}
		if value != "disabled" && value != "verify-full" {
			return errors.New("--tls must be disabled or verify-full")
		}
		request.tlsSeen = true
		request.tlsMode = value
	case "--tls-ca-file":
		if request.tlsCASeen {
			return errors.New("--tls-ca-file may be specified once")
		}
		request.tlsCASeen = true
		request.tlsCAFile = value
	case "--tls-server-name":
		if request.tlsServerSeen {
			return errors.New("--tls-server-name may be specified once")
		}
		request.tlsServerSeen = true
		request.tlsServerName = value
	default:
		return fmt.Errorf("unknown flag %q", name)
	}
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
	if request.tlsMode == "verify-full" && request.address == "" {
		return errors.New("--address is required")
	}
	return nil
}

func (request onlineConnectionRequest) readPassword(stdin io.Reader) (string, error) {
	if request.passwordStdin {
		return instance.ReadPassword("", stdin)
	}
	return instance.ReadPassword(request.passwordFile, stdin)
}

func (request onlineConnectionRequest) openDatabase(password string) (*sql.DB, error) {
	config := mysqldriver.NewConfig()
	config.User = request.account
	config.Passwd = password
	config.Net = "tcp"
	config.Addr = request.address
	config.Params = map[string]string{"allowCleartextPasswords": "false"}
	if request.tlsMode == "verify-full" {
		tlsConfig, err := onlineTLSConfig(request)
		if err != nil {
			return nil, err
		}
		name := "database-online-" + strings.ReplaceAll(request.address, ":", "-")
		if err := mysqldriver.RegisterTLSConfig(name, tlsConfig); err != nil {
			return nil, err
		}
		config.TLSConfig = name
	}
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func onlineTLSConfig(request onlineConnectionRequest) (*tls.Config, error) {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if request.tlsCAFile != "" {
		pem, err := os.ReadFile(request.tlsCAFile)
		if err != nil {
			return nil, fmt.Errorf("read TLS CA file: %w", err)
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("TLS CA file contains no certificates")
		}
	}
	serverName := request.tlsServerName
	if serverName == "" {
		host, _, err := net.SplitHostPort(request.address)
		if err != nil {
			return nil, fmt.Errorf("derive TLS server name: %w", err)
		}
		serverName = host
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: serverName}, nil
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
