package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jonbaldie/database/internal/credential"
	"github.com/jonbaldie/database/internal/instance"
)

const initializationUsage = "usage: database init DIRECTORY (--password-file FILE | --password-stdin) [--format=human|json]"

type initializationRequest struct {
	directory       string
	account         string
	accountProvided bool
	passwordFile    string
	passwordStdin   bool
}

func initialize(args []string, stdout, stderr io.Writer) int {
	return initializeWithReporter(args, stdout, stderr)
}

func initializeWithReporter(args []string, stdout, stderr io.Writer) int {
	reporter, filtered, err := newInitializationReporter(args, stdout, stderr)
	if err != nil {
		return initializationOutputFailure(reporter, err)
	}
	request, err := parseInitializationRequest(filtered)
	if err != nil {
		return initializationFailure(reporter, "invalid_input", err.Error())
	}
	return initializeRequest(request, reporter, stdout)
}

func newInitializationReporter(args []string, stdout, stderr io.Writer) (*operationReporter, []string, error) {
	output, filtered, err := parseCommandOutput(args)
	if err == nil && output.formatText {
		err = errors.New(initializationUsage)
	}
	if !output.resultSet && !output.formatSet && !output.progressSet {
		output.legacy = true
	}
	if output.legacy && !output.progressSet {
		output.progress = "none"
	}
	return newOperationReporter("init", output, stdout, stderr), filtered, err
}

func initializationOutputFailure(reporter *operationReporter, err error) int {
	if reporter.output.resultSet || reporter.output.formatSet || reporter.output.progressSet {
		reporter.output.result = "json"
	}
	return reporter.failure("invalid_input", "", err.Error(), nil)
}

func initializeRequest(request initializationRequest, reporter *operationReporter, stdout io.Writer) int {
	reporter.progress("preflight")
	if err := instance.ValidateInitializationTarget(request.directory); err != nil {
		return initializationFailure(reporter, "precondition", err.Error())
	}
	return initializeValidatedRequest(request, reporter, stdout)
}

func initializeValidatedRequest(request initializationRequest, reporter *operationReporter, stdout io.Writer) int {
	reporter.progress("initializing")
	password, err := request.readPassword(os.Stdin)
	if err != nil {
		return initializationFailure(reporter, "invalid_input", passwordInputFailure(err))
	}
	metadata, err := instance.Initialize(request.directory, request.account, password)
	if err != nil {
		return initializationFailure(reporter, initializationFailureClass(err), err.Error())
	}
	reporter.progress("validating")
	details := map[string]any{"instance_id": metadata.InstanceID, "data_directory": request.directory, "admin_account": metadata.AdminAccount, "state": metadata.State}
	if reporter.output.legacy && reporter.output.result == "human" {
		fmt.Fprintf(stdout, "initialized database instance %s\n", metadata.InstanceID)
		return 0
	}
	return reporter.success(details)
}

func initializationFailure(reporter *operationReporter, class, summary string) int {
	if reporter.output.legacy && reporter.output.result == "human" {
		reporter.output.result = "json"
	}
	return reporter.failure(class, "", summary, nil)
}

// initializationFailureClass reports credentials that violate the
// account-administration contract as invalid input; other failures are
// preconditions of the data directory.
func initializationFailureClass(err error) string {
	if errors.Is(err, credential.ErrInvalidAccountName) || errors.Is(err, credential.ErrInvalidPassword) {
		return "invalid_input"
	}
	return "precondition"
}

// passwordInputFailure names a password policy violation but keeps other read
// failures generic.
func passwordInputFailure(err error) string {
	if errors.Is(err, credential.ErrInvalidPassword) {
		return err.Error()
	}
	return "unable to read password"
}

func parseInitializationRequest(args []string) (initializationRequest, error) {
	request := initializationRequest{account: "admin"}
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
		return errors.New(initializationUsage)
	}
	return nil
}

func (request initializationRequest) readPassword(stdin io.Reader) (string, error) {
	if request.passwordStdin {
		return instance.ReadPassword("", stdin)
	}
	return instance.ReadPassword(request.passwordFile, stdin)
}
