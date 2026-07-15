package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/jonbaldie/database/prototype/query-explanation/contract"
)

const (
	bold  = "\x1b[1m"
	dim   = "\x1b[2m"
	reset = "\x1b[0m"
)

type state struct {
	analyzed bool
	json     bool
}

func main() {
	current := state{}
	reader := bufio.NewReader(os.Stdin)
	for {
		render(current)
		input, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		switch strings.ToLower(strings.TrimSpace(input)) {
		case "p":
			current.analyzed = false
		case "a":
			current.analyzed = true
		case "t":
			current.json = false
		case "j":
			current.json = true
		case "q":
			return
		}
	}
}

func render(current state) {
	fmt.Print("\x1b[2J\x1b[H")
	explanation := contract.Example(current.analyzed)
	mode := "EXPLAIN (plan only)"
	if current.analyzed {
		mode = "EXPLAIN ANALYZE (executes a read-only statement)"
	}
	format := "tabular projection"
	if current.json {
		format = "canonical JSON"
	}

	fmt.Printf("%sQuery explanation contract prototype%s\n\n", bold, reset)
	fmt.Printf("%sContract:%s JSON is normative; tabular output is a stable, documented projection.\n", bold, reset)
	fmt.Printf("%sAlternatives:%s bounded evidence at consequential choices; no full optimizer trace.\n", bold, reset)
	fmt.Printf("%sPlan evidence:%s operator identity, strategy, predicates, estimates, output properties, and provenance.\n", bold, reset)
	fmt.Printf("%sRuntime evidence:%s rows, timing, memory, spills, storage work, waits, and divergence.\n", bold, reset)
	fmt.Printf("%sAnalyze safety:%s execute only non-locking SELECT; discard rows; preserve session semantics.\n", bold, reset)
	fmt.Printf("%sTable:%s traditional MySQL prefix plus stable operator and runtime extensions.\n", bold, reset)
	fmt.Printf("%sJSON stability:%s preserve existing fields; additions are compatible; breaking changes use a new version.\n", bold, reset)
	fmt.Printf("%sLive inspection:%s non-blocking, explicitly partial snapshots of active connections.\n", bold, reset)
	fmt.Printf("%sStatus:%s contract validated; prototype retained only as decision evidence.\n", bold, reset)
	fmt.Printf("%sMode:%s %s\n", bold, reset, mode)
	fmt.Printf("%sFormat:%s %s\n\n", bold, reset, format)

	if current.json {
		fmt.Println(contract.RenderJSON(explanation))
	} else {
		fmt.Println(contract.RenderTable(explanation))
	}

	fmt.Printf("\n%s[p]%s plan  %s[a]%s analyze  %s[t]%s table  %s[j]%s JSON  %s[q]%s quit\n", bold, reset, bold, reset, bold, reset, bold, reset, bold, reset)
	fmt.Printf("%sType a key and press return.%s\n", dim, reset)
}
