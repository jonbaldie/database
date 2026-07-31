package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const configUsage = "Usage: database config validate [--config PATH] [configuration flags]"

func configCommand(args []string, stdout, stderr io.Writer) int {
	if isCommandHelp(args) {
		fmt.Fprintln(stdout, configUsage)
		return 0
	}
	if hasResultControl(args) {
		return configCommandWithReporter(args, stdout, stderr)
	}
	format, parsedArgs, err := configOutputFormat(args)
	operationID := newOperationID()
	if err != nil {
		return writeConfigFailure(stdout, "invalid_input", err.Error(), "config validate", operationID, format)
	}
	return validateConfiguration(parsedArgs, format, operationID, stdout)
}

func hasResultControl(args []string) bool {
	for _, arg := range args {
		if arg == "--json" || strings.HasPrefix(arg, "--result") || strings.HasPrefix(arg, "--progress") {
			return true
		}
	}
	return false
}

func configCommandWithReporter(args []string, stdout, stderr io.Writer) int {
	output, filtered, err := parseCommandOutput(args, false)
	operation := "config"
	if len(filtered) > 0 && filtered[0] != "" {
		operation += " " + filtered[0]
	}
	reporter := newOperationReporter(operation, output, stdout, stderr)
	if err != nil {
		if containsOutputControl(args) {
			reporter.output.result = "json"
		}
		return reporter.failure("invalid_input", "", err.Error(), nil)
	}
	if !isConfigValidation(filtered) {
		return reporter.failure("invalid_input", "", "config requires the validate operation", nil)
	}
	reporter.command = "config validate"
	reporter.progress("loading")
	reporter.progress("validating")
	config, err := resolveConfiguration(filtered[1:], os.Environ())
	if err != nil {
		return reporter.failure(configurationClass(err), "", err.Error(), nil)
	}
	legacy := configurationResult(config, reporter.id)
	return reporter.success(map[string]any{"settings": legacy["settings"]})
}

func validateConfiguration(args []string, format, operationID string, stdout io.Writer) int {
	if !isConfigValidation(args) {
		return invalidConfigurationOperation(args, format, operationID, stdout)
	}
	config, err := resolveConfiguration(args[1:], os.Environ())
	if err != nil {
		return writeConfigFailure(stdout, configurationClass(err), err.Error(), "config validate", operationID, format)
	}
	return writeConfigurationResult(stdout, config, operationID, format)
}

func isConfigValidation(args []string) bool {
	return len(args) > 0 && args[0] == "validate"
}

func invalidConfigurationOperation(args []string, format, operationID string, stdout io.Writer) int {
	operation := "config"
	if len(args) > 0 {
		operation += " " + args[0]
	}
	return writeConfigFailure(stdout, "invalid_input", "config requires the validate operation", operation, operationID, format)
}

func writeConfigurationResult(stdout io.Writer, config configuration, operationID, format string) int {
	if format != "json" {
		writeConfigurationHuman(stdout, config, operationID)
		return 0
	}
	result := configurationResult(config, operationID)
	result["operation"] = "config validate"
	result["success"] = true
	result["exit_class"] = "success"
	_ = json.NewEncoder(stdout).Encode(result)
	return 0
}

func configOutputFormat(args []string) (string, []string, error) {
	parser := outputFormatParser{format: "human", filtered: make([]string, 0, len(args))}
	for index, count := 0, len(args); index < count; {
		next, err := parser.consume(args, index)
		if err != nil {
			return parser.format, nil, err
		}
		index = next
	}
	return parser.format, parser.filtered, nil
}

type outputFormatParser struct {
	format   string
	filtered []string
	seen     bool
}

func (parser *outputFormatParser) consume(args []string, index int) (int, error) {
	argument := args[index]
	if !isOutputFormatArgument(argument) {
		parser.filtered = append(parser.filtered, argument)
		return index + 1, nil
	}
	if parser.seen {
		return 0, errors.New("repeated output format")
	}
	parser.seen = true
	value, next, err := outputFormatValue(args, index)
	if err != nil {
		return 0, err
	}
	return next, parser.set(value)
}

func isOutputFormatArgument(argument string) bool {
	return argument == "--format" || strings.HasPrefix(argument, "--format=")
}

func outputFormatValue(args []string, index int) (string, int, error) {
	argument := args[index]
	if argument != "--format" {
		return strings.TrimPrefix(argument, "--format="), index + 1, nil
	}
	if index+1 >= len(args) {
		return "", 0, errors.New("--format requires a value")
	}
	return args[index+1], index + 2, nil
}

func (parser *outputFormatParser) set(value string) error {
	format, err := normalizedOutputFormat(value)
	if err != nil {
		return err
	}
	parser.format = format
	return nil
}

func normalizedOutputFormat(value string) (string, error) {
	switch value {
	case "json":
		return "json", nil
	case "human", "text":
		return "human", nil
	default:
		return "", fmt.Errorf("format must be human, text, or json")
	}
}

func writeConfigFailure(stdout io.Writer, class, message, operation, operationID, format string) int {
	if format != "json" {
		fmt.Fprintf(stdout, "configuration invalid [%s] (operation_id=%s): %s\n", class, operationID, message)
		return operatorExitCode(class)
	}
	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"schema": "database.operator.result/v1", "operation": operation, "operation_id": operationID,
		"success": false, "exit_class": class, "diagnostic": message,
	})
	return operatorExitCode(class)
}

func writeConfigurationHuman(stdout io.Writer, config configuration, operationID string) {
	fmt.Fprintf(stdout, "configuration valid (operation_id=%s)\n", operationID)
	for _, name := range sortedConfigurationNames(config.values) {
		writeConfigurationSetting(stdout, name, config.values[name])
	}
}

func sortedConfigurationNames(values map[string]configurationValue) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func writeConfigurationSetting(stdout io.Writer, name string, setting configurationValue) {
	value := setting.value
	if name == "tls_private_key_file" && value != "" {
		value = "[redacted]"
	}
	fmt.Fprintf(stdout, "%s=%s (%s)\n", name, value, setting.source)
}
