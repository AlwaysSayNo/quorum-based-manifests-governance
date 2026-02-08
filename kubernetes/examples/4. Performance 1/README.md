# Performance Test 1: Admission Control Overhead

### Objective

Measure the additional latency over standard Kubernetes operations by the Qubmango validating webhook. This benchmark compares the time required for the **Kubernetes API Server** to process requests *with* and *without* the Qubmango webhook active.

### Scenario

This test uses a read-heavy model with the following distribution:
- **Reads (80%)**: Fetch deployments, namespaces, configmaps, or non-existing resources
  - 30% deployment reads
  - 30% namespace reads
  - 30% configmap reads
  - 10% non-existing resource reads
- **Updates (10%)**: Patch annotations on existing resources
- **Creates (7%)**: Create new resources
- **Deletes (3%)**: Remove existing resources

Each test run is pre-populated with initial state: *3 namespaces, each containing 1 Deployment and 1 ConfigMap* to ensure operations target valid existing resources.

### Test Protocol

1. **Input Generation**: Generate input files for batch sizes of **1, 10, 100, 500, 1000** commands.
2. **Repetition**: Each batch file generated **5 times** with different seeds. Then each file executed once. Then the arithmetic mean and minimize variance are calculated.
3. **Execution**: Each file is executed separately. Each command is executed individually, timing only the `kubectl` command execution (not the whole execution). The tests are run first without the Qubmango webhook (baseline) and then with the webhook active. Between each test run, the cluster state is reset by deleting all test namespaces to ensure consistency.

### Tools

Tools can be rebuild in this repository using the following commands: 

```bash
cd generator && GOWORK=off go build -o ../generator-bin .
cd ../executor && GOWORK=off go build -o ../executor-bin .
cd ..
```

---

## Prerequisites

1. Complete the steps in the [0. Setup README](../0.%20Setup/README.md).
2. Copy this scenario's `Taskfile.yaml`, `generator-bin`, and `executor-bin` into the root of your test git repository (the repository **ArgoCD** is watching).

**Note**: Evaluation assume you're running from your test git repository root where the `Taskfile.yaml` and binaries are located.

---

## Generator Tool

The generator creates a file with `INIT` and `MAIN` command blocks.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--read` | 80 | Read operations percentage (0-100) |
| `--update` | 10 | Update operations percentage (0-100) |
| `--create` | 7 | Create operations percentage (0-100) |
| `--delete` | 3 | Delete operations percentage (0-100) |
| `--n` | 100 | Number of commands in the MAIN block |
| `--seed` | 42 | Random seed (use 0 for time-based randomization) |
| `--out` | `input.txt` | Output file path |

**Note**: Percentages (`--read`, `--update`, `--create`, `--delete`) must sum to 100.

### Example

```bash
./generator-bin --n 100 --seed 123 --out input-100-1.txt
```

---

## Executor Tool

The executor runs the commands from a generated input file and measures execution time.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--input` | `input.txt` | Path to the generated input file |
| `--init-sleep` | 5 | Seconds to sleep after the INIT block |
| `--create-sleep` | 1 | Seconds to sleep after each create/apply command |

### Output

- **stderr**: Human-readable progress and summary
- **stdout**: CSV-like summary: `<count>,<milliseconds>,<microseconds>`

### Example

```bash
./executor-bin --input input-100-1.txt
```

---

## Test Execution

### Step 0: Prepare Environment

Create the `inputs` directory for generated test files.

```bash
task 0-prepare
```

### Step 1: Generate Input Files

Generate all 25 input files (5 batch sizes × 5 runs) using different seeds.

```bash
task 1-generate:all
```

Or generate for specific batch sizes:

```bash
task 1-generate:batch-1    # Generate 5 files for batch size 1
task 1-generate:batch-10   # Generate 5 files for batch size 10
task 1-generate:batch-100  # Generate 5 files for batch size 100
task 1-generate:batch-500  # Generate 5 files for batch size 500
task 1-generate:batch-1000 # Generate 5 files for batch size 1000
```

---

### Step 2: Execute Baseline Tests

Run the baseline tests **without** the Qubmango webhook installed.

Run all baseline tests (25 executions with automatic cleanup after each):

```bash
task 2-baseline:all
```

Or run for specific batch sizes:

```bash
task 2-baseline:batch-1    # Run 5 tests for batch size 1
task 2-baseline:batch-10   # Run 5 tests for batch size 10
task 2-baseline:batch-100  # Run 5 tests for batch size 100
task 2-baseline:batch-500  # Run 5 tests for batch size 500
task 2-baseline:batch-1000 # Run 5 tests for batch size 1000
```

Results are appended to `results-baseline.csv`.

---

### Step 3: Install Qubmango

1. Push the MRT manifest to the remote repository and trigger ArgoCD sync.

```bash
task 3-install:1-push-mrt
```

2. Verify the Qubmango controller is ready and the MRT is created.

```bash
task 3-install:2-verify-governance
```

---

### Step 4: Execute Webhook Tests (With Qubmango Webhook)

Run the same tests **with** the Qubmango webhook active.

Run all webhook tests (25 executions with automatic cleanup after each):

```bash
task 4-webhook:all
```

Or run for specific batch sizes:

```bash
task 4-webhook:batch-1    # Run 5 tests for batch size 1
task 4-webhook:batch-10   # Run 5 tests for batch size 10
task 4-webhook:batch-100  # Run 5 tests for batch size 100
task 4-webhook:batch-500  # Run 5 tests for batch size 500
task 4-webhook:batch-1000 # Run 5 tests for batch size 1000
```

Results are appended to `results-webhook.csv`.

---

## Results

The output files `results-baseline.csv` and `results-webhook.csv` contain CSV lines in the format:
```
<command_count>,<total_milliseconds>,<total_microseconds>
```

### Calculate Means

For each batch size, calculate the arithmetic mean of the 5 runs:

```bash
# Baseline
grep "^1," results-baseline.csv | awk -F, '{sum+=$2; total+=$1} END {print "Mean (ms):", sum/total}'
grep "^10," results-baseline.csv | awk -F, '{sum+=$2; total+=$1} END {print "Mean (ms):", sum/total}'
grep "^100," results-baseline.csv | awk -F, '{sum+=$2; total+=$1} END {print "Mean (ms):", sum/total}'
grep "^500," results-baseline.csv | awk -F, '{sum+=$2; total+=$1} END {print "Mean (ms):", sum/total}'
grep "^1000," results-baseline.csv | awk -F, '{sum+=$2; total+=$1} END {print "Mean (ms):", sum/total}'

# With webhook
grep "^1," results-webhook.csv | awk -F, '{sum+=$2; total+=$1} END {print "Mean (ms):", sum/total}'
grep "^10," results-webhook.csv | awk -F, '{sum+=$2; total+=$1} END {print "Mean (ms):", sum/total}'
grep "^100," results-webhook.csv | awk -F, '{sum+=$2; total+=$1} END {print "Mean (ms):", sum/total}'
grep "^500," results-webhook.csv | awk -F, '{sum+=$2; total+=$1} END {print "Mean (ms):", sum/total}'
grep "^1000," results-webhook.csv | awk -F, '{sum+=$2; total+=$1} END {print "Mean (ms):", sum/total}'
```

### Compare Overhead

Calculate the overhead percentage:
```
Overhead (%) = ((Mean_webhook - Mean_baseline) / Mean_baseline) × 100
```

---

## Cleanup

1. Remove any remaining performance test namespaces.

```bash
task 5-cleanup:1-remove-namespaces
```

2. Finally, clean up the governance initialization from the cluster:

```bash
task 5-cleanup:2-governance-init
```
