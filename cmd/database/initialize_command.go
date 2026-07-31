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
	directory       string
	account         string
	accountProvided bool
	passwordFile    string
	passwordStdin   bool
	format          string
	formatProvided  bool
}

func initialize(args []string, stdout, stderr io.Writer) int {
	if hasResultControl(args) {
		return initializeWithReporter(args, stdout, stderr)
	}
	request, err := parseInitializationRequest(args)
	if err != nil {
		return writeOperatorFailure(stdout, "init", newOperationID(), "invalid_input", 2, err.Error())
	}
	if err := instance.ValidateInitializationTarget(request.directory); err != nil {
		return writeOperatorFailure(stdout, "init", newOperationID(), "precondition", 3, err.Error())
	}
	password, err := request.readPassword(os.Stdin)
	if err != nil {
		return writeOperatorFailure(stdout, "init", newOperationID(), "invalid_input", 2, "unable to read password")
	}
	metadata, err := instance.Initialize(request.directory, request.account, password)
	if err != nil {
		return writeOperatorFailure(stdout, "init", newOperationID(), "precondition", 3, err.Error())
	}
	return writeInitializationSuccess(stdout, request, metadata)
}

func initializeWithReporter(args []string, stdout, stderr io.Writer) int {
	output, filtered, err := parseCommandOutput(args, true)
	reporter := newOperationReporter("init", output, stdout, stderr)
	if err != nil {
		if containsOutputControl(args) {
			reporter.output.result = "json"
		}
		return reporter.failure("invalid_input", "", err.Error(), nil)
	}
	request, err := parseInitializationRequest(filtered)
	if err != nil {
		return reporter.failure("invalid_input", "", err.Error(), nil)
	}
	reporter.progress("preflight")
	if err := instance.ValidateInitializationTarget(request.directory); err != nil {
		return reporter.failure("precondition", "", err.Error(), nil)
	}
	reporter.progress("initializing")
	password, err := request.readPassword(os.Stdin)
	if err != nil {
		return reporter.failure("invalid_input", "", "unable to read password", nil)
	}
	metadata, err := instance.Initialize(request.directory, request.account, password)
	if err != nil {
		return reporter.failure("precondition", "", err.Error(), nil)
	}
	return reporter.success(map[string]any{"instance_id": metadata.InstanceID, "data_directory": request.directory, "admin_account": metadata.AdminAccount})
}

func parseInitializationRequest(args []string) (initializationRequest, error) {
	request := initializationRequest{format: "human", account: "admin"}
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
	case "--data-directory":
		return request.setDirectoryValue(args, index, value, hasValue)
	case "--initial-account":
		return request.setAccount(args, index, value, hasValue)
	case "--password-file", "--initial-password-file":
		return request.setPasswordFile(args, index, value, hasValue)
	case "--password-stdin", "--initial-password-stdin":
		return index, request.setPasswordStdin(hasValue)
	case "--format":
		return request.setFormat(args, index, value, hasValue)
	default:
		return index, fmt.Errorf("unknown flag %q", name)
	}
}

func (request *initializationRequest) setDirectoryValue(args []string, index int, value string, hasValue bool) (int, error) {
	value, next, err := requiredInitializationValue(args, index, "--data-directory", value, hasValue)
	if err != nil {
		return index, err
	}
	return next, request.setDirectory(value)
}

func (request *initializationRequest) setAccount(args []string, index int, value string, hasValue bool) (int, error) {
	if request.accountProvided {
		return index, errors.New("initial account may be specified once")
	}
	value, next, err := requiredInitializationValue(args, index, "--initial-account", value, hasValue)
	if err != nil {
		return index, err
	}
	request.account = value
	request.accountProvided = true
	return next, nil
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
	result := operatorResult("init", newOperationID(), true, "success", "")
	result["instance_id"] = metadata.InstanceID
	result["data_directory"] = directory
	result["admin_account"] = metadata.AdminAccount
	return result
}
