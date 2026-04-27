# Code Crucible — Project Proposal

## 1. Overview

**Code Crucible** is a model-agnostic, general-purpose system for generating, evaluating, benchmarking, and evolving multiple implementations of a given task. It orchestrates AI coding agents, containerized execution environments, proxy/mock layers, and benchmarking tools to identify optimal solutions across multiple dimensions such as performance, memory usage, I/O efficiency, external call behavior, and cost.

Rather than producing a single “correct” solution, Code Crucible treats code as a **competitive and evolving artifact**, enabling systematic exploration of tradeoffs and continuous improvement over time.

---

## 2. Problem Statement

Modern AI coding tools excel at generating functional code but lack:

- Objective performance comparison across multiple implementations
- Reproducible benchmarking under controlled environments
- Multi-objective optimization, such as latency vs. memory vs. cost
- Persistent historical learning across runs
- Structured iteration beyond a single solution
- Safe, reproducible handling of external service calls

Developers are left manually testing, benchmarking, comparing, and iterating.

**Code Crucible solves this by introducing a structured, automated tournament and evolution loop.**

---

## 3. Core Concept

At its core, Code Crucible implements an iterative loop:

```text
Generate → Execute → Benchmark → Score → Archive → Evolve → Repeat
```

Each cycle produces multiple competing implementations, evaluates them under controlled conditions, and uses results to inform the next generation.

---

## 4. Goals

### Primary Goals

- Generate multiple valid implementations of a task
- Benchmark implementations under reproducible conditions
- Track performance across multiple metrics
- Track external calls through a proxy or mock layer
- Maintain a persistent archive of all attempts
- Iteratively improve solutions using prior results

### Secondary Goals

- Support multiple AI agents interchangeably
- Provide extensible benchmarking and evaluation systems
- Enable multi-objective optimization strategies
- Offer both CLI and automation-friendly workflows
- Support replayable, deterministic external dependency behavior

---

## 5. Non-Goals (v1)

- Building a new AI coding model
- Replacing existing agent frameworks
- Providing a hosted SaaS platform
- Fully autonomous self-improving AI research systems

---

## 6. System Architecture

### High-Level Flow

```text
Task Spec
  ↓
Agent Provider → Generate Candidates
  ↓
Execution Sandbox (Docker/Podman)
  ↓
Network Proxy / Mock Layer
  ↓
Evaluator (tests + benchmarks)
  ↓
Metric Collector
  ↓
Archive + Database
  ↓
Scoring + Selection
  ↓
Next Generation Prompt
```

---

## 7. Core Components

### 7.1 Agent Provider (Pluggable)

Responsible for generating candidate implementations.

Examples:

- Codex CLI
- Claude Code
- OpenHands
- Local LLMs
- Future agent frameworks

Interface concept:

```ts
generateCandidate(spec: TaskSpec, context: GenerationContext): Candidate
```

---

### 7.2 Execution Sandbox

Runs candidate code safely and reproducibly.

Implementation options:

- Docker / Podman containers
- Resource constraints for CPU, memory, and I/O
- Network isolation
- Mounted read-only task assets
- Isolated writable workspace per candidate

The sandbox should prevent candidate code from directly modifying framework internals, evaluator scripts, archive metadata, or unrelated host files.

---

### 7.3 Evaluator

Validates correctness and runs benchmarks.

Responsibilities:

- Run test suites
- Execute benchmark scripts
- Validate output correctness
- Produce structured results
- Fail closed when output is missing, malformed, or suspicious

Interface concept:

```ts
run(candidate: Candidate): EvaluationResult
```

---

### 7.4 External Call Proxy / Mock Layer

All candidate network access should be routed through a controlled proxy or mock layer. Candidate code should not call third-party services directly during evaluation unless explicitly permitted by the run policy.

This layer is a first-class part of the system rather than an optional add-on.

#### Purposes

**Measurement**

Track:

- Request count
- Retry count
- Failure count
- Target hostnames
- Protocols used
- Bytes sent and received
- Per-call latency
- Response status codes
- Cache hits
- Replay hits
- Estimated cost

**Safety**

Prevent candidates from:

- Exfiltrating data
- Spamming APIs
- Calling unexpected hosts
- Accidentally triggering expensive external services
- Winning by outsourcing work to uncontrolled infrastructure

**Reproducibility**

Support:

- Deterministic mocked responses
- Fixture-based replay
- Recorded-response playback
- Offline benchmark runs
- Stable comparisons between candidates

**Fairness**

Ensure that candidates are not rewarded for:

- Skipping required external work
- Calling unapproved services
- Using hidden external computation
- Depending on network timing variance
- Making uncontrolled live API calls

#### Recommended External Call Modes

```text
mock      → All external APIs are simulated locally.
record    → Real calls are made through the proxy and captured.
replay    → Previously recorded responses are served deterministically.
allowlist → Only approved hosts/protocols are reachable.
deny      → Network is disabled except local benchmark infrastructure.
```

#### Conceptual Flow

```text
candidate container
  ↓ HTTP(S), DNS, gRPC, SDK calls
local proxy / mock gateway
  ↓
allowed live service, recorded fixture, or deterministic mock
```

#### Proxy/Mock Implementation Options

Potential building blocks:

- Local HTTP proxy
- MITM proxy for HTTP(S), where appropriate and explicitly configured
- Local fake service containers
- DNS override to route known services to local mocks
- SDK-specific adapters for high-value APIs
- VCR-style record/replay fixtures
- Service virtualization tools

The framework should expose this as an abstract interface so the implementation can evolve over time.

Interface concept:

```ts
prepareExternalEnvironment(policy: ExternalPolicy): ExternalEnvironment
collectExternalTrace(run: EvaluationRun): ExternalCallTrace
```

---

### 7.5 Metric Collector

Captures system-level and behavior-level performance metrics without requiring candidate code to instrument itself.

#### CPU Metrics

- Wall time
- User CPU time
- System CPU time
- Single-core utilization
- Multi-core utilization
- Context switches
- Load/run queue signals where available

#### Memory Metrics

- Peak RSS
- Page faults
- Allocation patterns when language tooling supports it

#### I/O Metrics

- Bytes read and written
- Read/write syscall counts
- `fsync` count
- Temporary files created
- Disk wait time where available

#### External Call Metrics

Collected through the proxy/mock layer:

- Request count
- Target hosts
- Total bytes sent and received
- Latency per call
- Failures and retries
- Estimated API cost
- Cache hits and replay hits

Potential tooling:

- `/usr/bin/time -v`
- `perf stat`
- `pidstat`
- `iostat`
- `strace -c`
- cgroup v2 stats
- Docker/Podman stats
- Proxy logs and trace output

---

### 7.6 Archive System

Stores all historical data as immutable artifacts plus a queryable index.

Artifacts per candidate:

- Source code
- Prompt and generation context
- Agent/model metadata
- Logs and outputs
- Benchmark results
- System metrics
- External call traces and proxy logs
- Verdicts
- Lineage metadata, including parent candidates

Recommended storage:

- Filesystem for artifacts
- SQLite for index and metadata

Example layout:

```text
runs/
  task-id/
    round-0001/
      candidate-0001/
        src/
        prompt.md
        design.md
        test.log
        benchmark.json
        metrics.json
        external-trace.json
        verdict.json
      candidate-0002/
        ...
    leaderboard.sqlite
```

---

### 7.7 Scoring Engine

Ranks candidates based on one or more metrics.

Supports:

- Single-objective ranking
- Multi-objective scoring
- Constraint-based filtering
- Category-specific winners
- Regression detection
- Disqualification for policy violations

Example scoring goal:

```text
Minimize p95 latency subject to:
- memory_peak < 100MB
- external_cost <= $0.01
- correctness_passed = true
- external_policy_passed = true
```

A leaderboard should be able to show different winners:

```text
Fastest single-threaded: Candidate 12
Best multi-core throughput: Candidate 18
Lowest memory use: Candidate 07
Lowest external cost: Candidate 03
Best balanced score: Candidate 21
```

---

### 7.8 Evolution Strategy

Determines how new candidates are generated.

Strategies can:

- Select top performers
- Select diverse candidates
- Preserve category-specific winners
- Hybridize approaches
- Avoid known failure patterns
- Explore novel implementations
- Penalize policy violations
- Encourage candidates that reduce external call cost

Example next-round instruction:

```text
Candidate A is fastest but uses too much memory.
Candidate B is slower but uses 70% less memory.
Candidate C makes half as many external calls but has worse p95 latency.
Candidate D failed correctness on Unicode input.

Generate five new variants:
- two derived from A with reduced memory use
- one derived from B with improved throughput
- one derived from C with lower latency
- one novel approach avoiding all known failure modes
```

---

## 8. Data Model (Simplified)

### Candidate

```text
id
parent_ids
round
agent
model
prompt_hash
source_path
created_at
```

### Metrics

```text
runtime_mean
p95_latency
memory_peak
cpu_usage_single_core
cpu_usage_multi_core
io_bytes_read
io_bytes_written
external_call_count
external_latency_ms
external_cost_estimate
```

### ExternalCallTrace

```text
candidate_id
run_id
mode: mock | record | replay | allowlist | deny
request_count
unique_hosts
bytes_sent
bytes_received
retry_count
failure_count
estimated_cost
trace_path
policy_passed
policy_violations
```

### Verdict

```text
correctness_passed
benchmark_passed
external_policy_passed
errors
warnings
notes
```

---

## 9. CLI Design (Initial)

```bash
crucible init
crucible run --task task.md --variants 5 --rounds 3
crucible leaderboard
crucible inspect <candidate-id>
crucible replay <candidate-id>
crucible proxy-report <run-id>
```

Example with explicit external call mode:

```bash
crucible run \
  --task task.md \
  --variants 6 \
  --rounds 5 \
  --evaluator ./bench.sh \
  --metric p95_latency \
  --external-mode replay \
  --external-fixtures ./fixtures/http
```

---

## 10. Example Workflow

System behavior:

1. Generate six implementations.
2. Run tests and benchmarks in sandboxed containers.
3. Route all external calls through the proxy/mock gateway.
4. Store results, logs, metrics, and external call traces.
5. Rank candidates.
6. Generate the next round using best, diverse, and instructive failed candidates.
7. Repeat until max rounds or target metric is reached.

---

## 11. Key Differentiators

- Multi-candidate generation rather than single-solution generation
- Objective benchmarking under controlled environments
- Persistent archive with lineage tracking
- Multi-metric evaluation across performance, memory, CPU, I/O, external calls, and cost
- Model-agnostic architecture
- Proxy/mock-mediated external dependency tracking
- Reproducible and replayable results

---

## 12. MVP Scope

### Must Have

- CLI interface
- Single agent integration
- Docker-based execution
- Basic benchmarking support
- Artifact storage
- Leaderboard output
- Iterative rounds with simple selection strategy
- External call policy with at least `deny`, `allowlist`, and `mock/replay` modes

### Nice to Have

- Multi-agent support
- Advanced metrics using `perf`, `strace`, and cgroups
- HTTP/gRPC proxying with cost models
- Multi-objective scoring
- HTML reporting
- VCR-style fixture recording and replay

---

## 13. Future Enhancements

- Distributed execution
- Web UI dashboard
- Plugin ecosystem
- Advanced evolution strategies
- Hardware-aware optimization
- CI/CD integration
- Cross-run archive search and recommendation
- Cost-aware optimization for API-heavy workloads
- Protocol-specific external call adapters for common APIs

---

## 14. Risks & Challenges

- Ensuring deterministic and reproducible benchmarks
- Preventing agent-generated code from bypassing evaluation
- Managing LLM and external API costs
- Handling diverse languages and environments
- Avoiding overfitting to benchmarks
- Capturing network behavior consistently across protocols and SDKs
- Preventing external-call mocks from becoming unrealistic

---

## 15. Summary

Code Crucible introduces a new paradigm:

> Treat code as a competitive, evolving artifact optimized through measurement.

By combining AI generation, rigorous benchmarking, proxy-mediated external dependency tracking, and iterative improvement, it enables developers to discover not just working solutions, but optimal solutions under real-world constraints.

---

## 16. Proposed Tagline

**Forge better code through pressure, measurement, and iteration.**
