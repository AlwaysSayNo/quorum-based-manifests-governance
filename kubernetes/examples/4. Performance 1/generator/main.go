package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
)

const (
	initHeader = "### INIT ###"
	mainHeader = "### MAIN ###"
)

const (
	actionRead   = 0
	actionUpdate = 1
	actionCreate = 2
	actionDelete = 3
)

func main() {
	var (
		readPct   int
		updatePct int
		createPct int
		deletePct int
		count     int
		seed      int64
		outFile   string
	)

	flag.IntVar(&readPct, "read", 80, "read percentage (0-100)")
	flag.IntVar(&updatePct, "update", 10, "update percentage (0-100)")
	flag.IntVar(&createPct, "create", 7, "create percentage (0-100)")
	flag.IntVar(&deletePct, "delete", 3, "delete percentage (0-100)")
	flag.IntVar(&count, "n", 100, "number of commands in the MAIN block")
	flag.Int64Var(&seed, "seed", 42, "random seed (0 for time based)")
	flag.StringVar(&outFile, "out", "input.txt", "output file path")
	flag.Parse()

	// === Input validation ===

	if readPct+updatePct+createPct+deletePct != 100 {
		fmt.Fprintf(os.Stderr, "Error: percentages must sum to 100, got %d\n",
			readPct+updatePct+createPct+deletePct)
		os.Exit(1)
	}

	if count <= 0 {
		fmt.Fprintf(os.Stderr, "Error: number of commands must be positive, got %d\n", count)
		os.Exit(1)
	}

	if seed == 0 {
		seed = rand.Int63()
	}
	rand.Seed(seed)

	if outFile == "" {
		fmt.Fprintln(os.Stderr, "Error: output file path is required")
		os.Exit(1)
	}

	// === Generation ===

	s := newState()
	var lines []string

	// INIT block
	generateInit(s, &lines)

	// Build action bucket based on weights
	bucket := createBucket(readPct, updatePct, createPct, deletePct)

	// MAIN block
	generateMain(s, bucket, &lines, count)

	// Write output
	output := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(outFile, []byte(output), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing file: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Generated %d MAIN commands → %s (seed=%d)\n", count, outFile, seed)
}

// Build weighted bucket: 0=read, 1=update, 2=create, 3=delete
func createBucket(readPct, updatePct, createPct, deletePct int) []int {
	var bucket []int
	for range readPct {
		bucket = append(bucket, actionRead)
	}
	for range updatePct {
		bucket = append(bucket, actionUpdate)
	}
	for range createPct {
		bucket = append(bucket, actionCreate)
	}
	for range deletePct {
		bucket = append(bucket, actionDelete)
	}
	return bucket
}

func generateInit(s *state, lines *[]string) {
	*lines = append(*lines, initHeader)

	initNS := []string{"perf-ns-init-1", "perf-ns-init-2", "perf-ns-init-3"}
	for _, ns := range initNS {
		s.addNamespace(ns)
		*lines = append(*lines, fmt.Sprintf("kubectl create namespace %s", ns))
	}
	for _, ns := range initNS {
		depName := s.nextDeploy()
		s.addDeploy(depName, ns)
		manifest := fmt.Sprintf(deploymentTemplate, depName, ns, depName, depName)
		*lines = append(*lines, fmt.Sprintf("kubectl apply -f - <<'EOF'\n%s\nEOF", manifest))
	}
	for _, ns := range initNS {
		cmName := s.nextCM()
		s.addCM(cmName, ns)
		*lines = append(*lines, fmt.Sprintf("kubectl create configmap %s -n %s --from-literal=key=value", cmName, ns))
	}
}

func generateMain(s *state, bucket []int, lines *[]string, count int) {
	*lines = append(*lines, mainHeader)

	for range count {
		action := bucket[rand.Intn(len(bucket))]
		var cmd string
		switch action {
		case actionRead:
			cmd = cmdRead(s)
		case actionUpdate:
			cmd = cmdUpdate(s)
		case actionCreate:
			cmd = cmdCreate(s)
		case actionDelete:
			cmd = cmdDelete(s)
		}
		*lines = append(*lines, cmd)
	}
}
