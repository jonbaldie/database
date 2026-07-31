package main

import "io"

// commandInvocation is the stable boundary between command selection and a
// workflow. Workflows continue to receive their established arguments and
// streams, while individual command implementations can now move independently.
type commandInvocation struct {
	args   []string
	stdout io.Writer
	stderr io.Writer
}

type commandHandler func(commandInvocation) int

func commandHandlerFor(name string) (commandHandler, bool) {
	switch name {
	case "version":
		return versionCommand, true
	case "init":
		return initializeCommand, true
	case "backup", "restore", "upgrade", "data", "shutdown":
		return operatorCommandHandler, true
	case "config":
		return configCommandHandler, true
	case "serve":
		return serveCommand, true
	case "help", "--help", "-h":
		return helpCommand, true
	default:
		return nil, false
	}
}

func versionCommand(invocation commandInvocation) int {
	return version(invocation.args[1:], invocation.stdout, invocation.stderr)
}

func initializeCommand(invocation commandInvocation) int {
	return initialize(invocation.args[1:], invocation.stdout, invocation.stderr)
}

func operatorCommandHandler(invocation commandInvocation) int {
	return operatorCommand(invocation.args, invocation.stdout, invocation.stderr)
}

func configCommandHandler(invocation commandInvocation) int {
	return configCommand(invocation.args[1:], invocation.stdout, invocation.stderr)
}

func serveCommand(invocation commandInvocation) int {
	return serve(invocation.args[1:], invocation.stdout, invocation.stderr)
}

func helpCommand(invocation commandInvocation) int {
	usage(invocation.stdout)
	return 0
}
