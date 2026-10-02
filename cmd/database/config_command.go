package main

import (
	"fmt"
	"io"
	"os"
	"sort"
)

const configUsage = "Usage: database config validate [--config PATH] [configuration flags]"

func configCommand(args []string, stdout, stderr io.Writer) int {
	if isCommandHelp(args) {
		fmt.Fprintln(stdout, configUsage)
		return 0
	}
	output, filtered, err := parseCommandOutput(args)
	if err != nil {
		return configOutputFailure(output, err, stdout, stderr)
	}
	operationID := newOperationID()
	if needsConfigReporter(output) {
		return configCommandWithReporter(output, filtered, stdout, stderr)
	}
	return validateConfiguration(filtered, operationID, stdout)
}

func configOutputFailure(output commandOutput, err error, stdout, stderr io.Writer) int {
	if output.formatSet && output.result == "human" && !output.resultSet && !output.progressSet {
		return writeConfigFailure(stdout, "invalid_input", err.Error(), newOperationID())
	}
	if output.resultSet || output.formatSet || output.progressSet {
		output.result = "json"
	}
	reporter := newOperationReporter("config validate", output, stdout, stderr)
	return reporter.failure("invalid_input", "", err.Error(), nil)
}

func needsConfigReporter(output commandOutput) bool {
	return output.result == "json" || output.resultSet || output.progressSet
}

func configCommandWithReporter(output commandOutput, filtered []string, stdout, stderr io.Writer) int {
	operation := "config"
	if len(filtered) > 0 && filtered[0] != "" {
		operation += " " + filtered[0]
	}
	reporter := newOperationReporter(operation, output, stdout, stderr)
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
	return reporter.success(map[string]any{"settings": configurationSettings(config)})
}

func validateConfiguration(args []string, operationID string, stdout io.Writer) int {
	if !isConfigValidation(args) {
		return writeConfigFailure(stdout, "invalid_input", "config requires the validate operation", operationID)
	}
	config, err := resolveConfiguration(args[1:], os.Environ())
	if err != nil {
		return writeConfigFailure(stdout, configurationClass(err), err.Error(), operationID)
	}
	writeConfigurationHuman(stdout, config, operationID)
	return 0
}

func isConfigValidation(args []string) bool {
	return len(args) > 0 && args[0] == "validate"
}

func writeConfigFailure(stdout io.Writer, class, message, operationID string) int {
	fmt.Fprintf(stdout, "configuration invalid [%s] (operation_id=%s): %s\n", class, operationID, message)
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
