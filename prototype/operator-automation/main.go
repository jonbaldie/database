package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type screenMode int

const (
	contractMode screenMode = iota
	progressMode
	resultMode
)

func main() {
	reader := bufio.NewReader(os.Stdin)
	commandIndex := 0
	phaseIndex := 0
	failure := false
	mode := contractMode

	for {
		command := commands[commandIndex]
		if len(command.progressPhases) > 0 && phaseIndex >= len(command.progressPhases) {
			phaseIndex = 0
		}
		render(command, commandIndex, phaseIndex, failure, mode)
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "n":
			commandIndex = (commandIndex + 1) % len(commands)
			phaseIndex = 0
		case "b":
			commandIndex = (commandIndex - 1 + len(commands)) % len(commands)
			phaseIndex = 0
		case "f":
			failure = !failure
		case "p":
			mode = progressMode
			if len(command.progressPhases) > 0 {
				phaseIndex = (phaseIndex + 1) % len(command.progressPhases)
			}
		case "r":
			mode = resultMode
		case "c":
			mode = contractMode
		case "q":
			return
		}
	}
}

func render(c commandContract, commandIndex, phaseIndex int, failure bool, mode screenMode) {
	fmt.Print("\033[2J\033[H")
	fmt.Printf("\033[1mTHROWAWAY: v0.1 operator automation contract\033[0m\n")
	fmt.Printf("\033[2mCommand %d/%d · %s · scenario: %s\033[0m\n\n", commandIndex+1, len(commands), c.name, scenarioName(failure))

	switch mode {
	case contractMode:
		fmt.Println("\033[1mExact inputs\033[0m")
		for _, input := range c.inputs {
			fmt.Printf("  • %s\n", input)
		}
		fmt.Printf("\n\033[1mSecret boundary\033[0m\n  %s\n", c.secret)
		phases := strings.Join(c.progressPhases, " → ")
		if phases == "" {
			phases = "none; command is expected to return its terminal result immediately"
		}
		fmt.Printf("\n\033[1mProgress phases\033[0m\n  %s\n", phases)
		fmt.Println("\n\033[1mSuccessful details fields\033[0m")
		fmt.Printf("  %s\n", strings.Join(c.successFields, ", "))
		fmt.Printf("\n\033[1mProbe failure\033[0m\n  exit %d / %s / %s\n  %s\n", c.failure.code, c.failure.exitClass, c.failure.diagnostic, c.failure.meaning)
	case progressMode:
		fmt.Println("\033[1mJSON Lines progress record (standard error)\033[0m")
		if len(c.progressPhases) == 0 {
			fmt.Println("  No progress record for this command.")
		} else {
			printJSON(progressRecord(c, phaseIndex))
		}
		fmt.Println("\n\033[2mProgress is advisory. Only a terminal result plus exit code completes a workflow.\033[0m")
	case resultMode:
		fmt.Println("\033[1mTerminal JSON result (standard output)\033[0m")
		printJSON(resultRecord(c, failure))
	}

	fmt.Println("\n\033[1m[n]\033[0m next  \033[1m[b]\033[0m back  \033[1m[f]\033[0m success/failure  \033[1m[c]\033[0m contract  \033[1m[p]\033[0m next progress  \033[1m[r]\033[0m result  \033[1m[q]\033[0m quit")
	fmt.Print("> ")
}

func printJSON(value any) {
	encoded, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(encoded))
}

func scenarioName(failure bool) string {
	if failure {
		return "failure"
	}
	return "success"
}
