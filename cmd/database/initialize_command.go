package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jonbaldie/database/internal/instance"
)

type initializationRequest struct {
	directory      string
	passwordFile   string
	passwordStdin  bool
	format         string
	formatProvided bool
}

func initialize(args []string, stdout, stderr io.Writer) int {
	request, err := parseInitializationRequest(args)
	if err != nil {
		return writeOperatorFailure(stdout, "init", newOperationID(), "invalid_input", 2, err.Error())
	}
	if request.directoryIsOccupied() {
		return writeOperatorFailure(stdout, "init", newOperationID(), "precondition", 3, "data directory is not empty")
	}
	password, err := request.readPassword(os.Stdin)
	if err != nil {
		return writeOperatorFailure(stdout, "init", newOperationID(), "invalid_input", 2, "unable to read password")
	}
	metadata, err := instance.Initialize(request.directory, "admin", password)
	if err != nil {
		return writeOperatorFailure(stdout, "init", newOperationID(), "precondition", 3, err.Error())
	}
	return writeInitializationSuccess(stdout, request, metadata)
}

func (request initializationRequest) directoryIsOccupied() bool {
	info, err := os.Stat(request.directory)
	if err != nil || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(request.directory)
	return err == nil && len(entries) != 0
}

func parseInitializationRequest(args []string) (initializationRequest, error) {
	request := initializationRequest{format: "human"}
	argumentCount := len(args)
	for index := 0; index < argumentCount; index++ {
		nextIndex, err := request.consume(args, index)
		if err != nil {
			return initializationRequest{}, err
		}
		index = nextIndex
	}
	if err := request.validate(); err != nil {
		return initializationRequest{}, err
	}
	return request, nil
}

func (request *initializationRequest) consume(args []string, index int) (int, error) {
	argument := args[index]
	if !strings.HasPrefix(argument, "-") {
		return index, request.setDirectory(argument)
	}
	if strings.HasPrefix(argument, "--password=") {
		return index, errors.New("inline passwords are not supported")
	}
	name, value, hasValue := strings.Cut(argument, "=")
	switch name {
	case "--password-file":
		return request.setPasswordFile(args, index, value, hasValue)
	case "--password-stdin":
		return index, request.setPasswordStdin(hasValue)
	case "--format":
		return request.setFormat(args, index, value, hasValue)
	default:
		return index, fmt.Errorf("unknown flag %q", name)
	}
}

func (request *initializationRequest) setDirectory(directory string) error {
	if request.directory != "" {
		return errors.New("multiple data directories")
	}
	request.directory = directory
	return nil
}

func (request *initializationRequest) setPasswordFile(args []string, index int, value string, hasValue bool) (int, error) {
	if request.passwordFile != "" || request.passwordStdin {
		return index, errors.New("password input may be specified once")
	}
	value, nextIndex, err := requiredInitializationValue(args, index, "--password-file", value, hasValue)
	if err != nil {
		return index, err
	}
	request.passwordFile = value
	return nextIndex, nil
}

func (request *initializationRequest) setPasswordStdin(hasValue bool) error {
	if hasValue {
		return errors.New("--password-stdin does not take a value")
	}
	if request.passwordFile != "" || request.passwordStdin {
		return errors.New("password input may be specified once")
	}
	request.passwordStdin = true
	return nil
}

func (request *initializationRequest) setFormat(args []string, index int, value string, hasValue bool) (int, error) {
	if request.formatProvided {
		return index, errors.New("--format may be specified once")
	}
	value, nextIndex, err := requiredInitializationValue(args, index, "--format", value, hasValue)
	if err != nil {
		return index, err
	}
	request.formatProvided = true
	request.format = value
	return nextIndex, nil
}

func requiredInitializationValue(args []string, index int, name, value string, hasValue bool) (string, int, error) {
	if hasValue && value != "" {
		return value, index, nil
	}
	if hasValue {
		return "", index, fmt.Errorf("%s requires a non-empty value", name)
	}
	nextIndex := index + 1
	if nextIndex >= len(args) || strings.HasPrefix(args[nextIndex], "--") {
		return "", index, fmt.Errorf("%s requires a non-empty value", name)
	}
	return args[nextIndex], nextIndex, nil
}

func (request initializationRequest) validate() error {
	if request.directory == "" || request.passwordFile == "" && !request.passwordStdin {
		return errors.New("usage: database init DIRECTORY (--password-file FILE | --password-stdin) [--format=human|json]")
	}
	if request.format != "human" && request.format != "json" {
		return errors.New("usage: database init DIRECTORY (--password-file FILE | --password-stdin) [--format=human|json]")
	}
	return nil
}

func (request initializationRequest) readPassword(stdin io.Reader) (string, error) {
	if request.passwordStdin {
		return instance.ReadPassword("", stdin)
	}
	return instance.ReadPassword(request.passwordFile, stdin)
}

func writeInitializationSuccess(stdout io.Writer, request initializationRequest, metadata instance.Metadata) int {
	if request.format == "json" {
		_ = json.NewEncoder(stdout).Encode(initializationResult(request.directory, metadata))
		return 0
	}
	fmt.Fprintf(stdout, "initialized database instance %s\n", metadata.InstanceID)
	return 0
}

func initializationResult(directory string, metadata instance.Metadata) map[string]any {
	return map[string]any{
		"schema": "database.operator.result/v1", "operation": "init", "operation_id": newOperationID(),
		"success": true, "exit_class": "success", "instance_id": metadata.InstanceID,
		"data_directory": directory, "admin_account": metadata.AdminAccount,
	}
}
