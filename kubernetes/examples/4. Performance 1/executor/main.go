package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	initHeader = "### INIT ###"
	mainHeader = "### MAIN ###"
)

// main executes the commands from the input file and measures the execution time of the MAIN block.
// It prints a general logs to stderr and a CSV-like line to stdout for easy parsing.
func main() {
	var (
		inputPath   string
		initSleep   int
		createSleep int
	)

	flag.StringVar(&inputPath, "input", "input.txt", "path to the generated input file")
	flag.IntVar(&initSleep, "init-sleep", 5, "seconds to sleep after the INIT block")
	flag.IntVar(&createSleep, "create-sleep", 1, "seconds to sleep after each create command")
	flag.Parse()

	// === Validate input ===

	if inputPath == "" {
		fmt.Fprintln(os.Stderr, "Error: input file path is required")
		os.Exit(1)
	}

	if initSleep < 0 {
		fmt.Fprintln(os.Stderr, "Error: init-sleep must be non-negative")
		os.Exit(1)
	}

	if createSleep < 0 {
		fmt.Fprintln(os.Stderr, "Error: create-sleep must be non-negative")
		os.Exit(1)
	}

	// === Parse input file ===

	initCmds, mainCmds, err := parseCommands(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing input file: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "INIT commands: %d, MAIN commands: %d\n", len(initCmds), len(mainCmds))

	// === Execute init block ===

	executeInitCommands(initCmds, createSleep)

	// Sleep after init block to let the cluster stabilize before starting the main block
	fmt.Fprintf(os.Stderr, "Sleeping %d seconds after INIT...\n", initSleep)
	time.Sleep(time.Duration(initSleep) * time.Second)

	// === Execute MAIN block ===

	totalDuration := executeMainCommands(mainCmds, createSleep)

	// === Report ===

	printReport(totalDuration, mainCmds)
}

func executeInitCommands(cmds []string, createSleep int) {
	fmt.Fprintln(os.Stderr, "=== Executing INIT block ===")
	for i, cmd := range cmds {
		fmt.Fprintf(os.Stderr, "[INIT %d/%d] %s\n", i+1, len(cmds), firstLine(cmd))
		if _, err := runCommand(cmd); err != nil {
			fmt.Fprintf(os.Stderr, "	Warning: init command failed: %v\n", err)
		}
		if isCreateCommand(cmd) {
			time.Sleep(time.Duration(createSleep) * time.Second)
		}
	}
}

func executeMainCommands(cmds []string, createSleep int) time.Duration {
	fmt.Fprintln(os.Stderr, "=== Executing MAIN block ===")
	var totalDuration time.Duration

	for i, cmd := range cmds {
		fmt.Fprintf(os.Stderr, "[MAIN %d/%d] %s\n", i+1, len(cmds), firstLine(cmd))
		d, err := runCommand(cmd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "	Warning: command failed: %v (took %v)\n", err, d)
		}
		totalDuration += d

		if isCreateCommand(cmd) {
			time.Sleep(time.Duration(createSleep) * time.Second)
		}
	}
	return totalDuration
}

func printReport(totalDuration time.Duration, mainCmds []string) {
	fmt.Fprintln(os.Stderr, "=== Results ===")
	fmt.Fprintf(os.Stderr, "Total MAIN commands : %d\n", len(mainCmds))
	fmt.Fprintf(os.Stderr, "Total execution time: %v\n", totalDuration)
	if len(mainCmds) > 0 {
		avg := totalDuration / time.Duration(len(mainCmds))
		fmt.Fprintf(os.Stderr, "Average per command : %v\n", avg)
	}

	// CSV-like output on stdout
	fmt.Printf("%d,%v,%d\n", len(mainCmds), totalDuration.Milliseconds(), totalDuration.Microseconds())
}

// Helper functions

func runCommand(cmd string) (time.Duration, error) {
	c := exec.Command("bash", "-c", cmd)
	c.Stdout = os.Stderr
	c.Stderr = os.Stderr

	start := time.Now()
	err := c.Run()
	return time.Since(start), err
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx] + " ..."
	}
	return s
}

func isCreateCommand(cmd string) bool {
	first := firstLine(cmd)
	return strings.Contains(first, "kubectl create") ||
		strings.Contains(first, "kubectl apply")
}
