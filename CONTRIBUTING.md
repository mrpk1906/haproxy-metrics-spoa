# Contributing to haproxy-metrics-spoa

Thank you for your interest in contributing to `haproxy-metrics-spoa`! We welcome contributions, bug reports, and suggestions from the community.

Please take a moment to review these guidelines before submitting an issue or pull request.

---

## Code of Conduct

We are committed to providing an open, welcoming, and harassment-free environment for everyone. Please be respectful and constructive in all discussions, issues, and pull requests.

---

## Prerequisites

Before building or testing the project, ensure you have installed:

- **Go**: Version `1.27.0` or higher
- **Make**: GNU Make
- **Docker & Docker Compose**: Optional for unit tests, required for running End-to-End (E2E) tests
- **Git**: For version control

---

## Development Setup

1. **Clone the repository:**
   ```bash
   git clone https://github.com/mrpk1906/haproxy-metrics-spoa.git
   cd haproxy-metrics-spoa
   ```

2. **Verify dependencies and environment:**
   ```bash
   go mod download
   go mod verify
   ```

3. **Build the daemon binary:**
   ```bash
   make build
   # Binary will be placed in bin/haproxy-metrics-spoa
   ```

---

## Testing & Quality Assurance

We maintain strict test integrity. Before submitting any changes, verify that all test suites pass cleanly.

### Unit Tests
Run standard unit tests:
```bash
make test
# or: go test -v ./...
```

### Race Detection
Run all unit tests with Go's race detector enabled:
```bash
make test-race
# or: go test -race -v ./...
```

### Code Formatting and Static Analysis
Ensure code adheres to standard Go conventions:
```bash
make fmt   # runs: go fmt ./...
make vet   # runs: go vet ./...
```

### End-to-End (E2E) Tests
The E2E test suite validates real SPOP traffic between HAProxy and `haproxy-metrics-spoa` across both TCP and UNIX domain socket transports using Docker:
```bash
make test-e2e
# or: go test -tags=e2e -v -timeout=120s ./test/e2e/...
```

To manually manage the E2E Docker stack during development:
```bash
make docker-e2e-up    # start containers
make docker-e2e-down  # stop and remove containers
```

---

## Architecture & Coding Standards

`haproxy-metrics-spoa` is designed for high-throughput, low-latency production environments. All contributions must respect these architectural principles:

1. **Zero CGo Dependency:**
   - The daemon must remain 100% pure Go. Never introduce CGo or native library bindings.

2. **Zero Client Latency Overhead:**
   - All SPOP message handling must execute purely in-memory.
   - Never perform disk I/O, external network calls, or blocking operations on the SPOE processing hot path.

3. **Cardinality Explosion Guard:**
   - Never expose raw, unvalidated host headers directly to Prometheus metrics labels.
   - All incoming hostnames must be sanitized and checked via `normalizer.Guard.Normalize()` before recording metrics.

4. **Concurrency & Resource Management:**
   - Protect shared mutable state using `sync.RWMutex` (prefer read locks for hot paths) or atomic operations (`sync/atomic`).
   - Propagate `context.Context` for cancellation and bounded timeouts across server lifecycle methods.
   - Always release pooled SPOP resources (e.g., `encoding.ReleaseMessage`, `encoding.ReleaseKVScanner`) using `defer`.
   - Cleanly unlink UNIX domain socket files upon server shutdown.

5. **Error Handling:**
   - Return explicit errors wrapped with context: `fmt.Errorf("...: %w", err)`.
   - Avoid swallowing errors or using blank identifiers without clear commentary.

---

## Commit Message Conventions

We follow the [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) specification. This ensures consistent git logs and automated release notes.

Format:
```text
<type>(<scope>): <short description>

[optional body]

[optional footer(s)]
```

### Allowed Types
- `feat`: A new feature or capability
- `fix`: A bug fix
- `docs`: Documentation changes only
- `test`: Adding or updating tests
- `refactor`: Code change that neither fixes a bug nor adds a feature
- `perf`: Code changes that improve performance
- `build`: Changes to build targets, Dockerfile, or Makefile
- `ci`: Changes to CI/CD workflows and automation
- `chore`: Routine maintenance, dependency updates, or gitignore changes

### Examples
- `feat(normalizer): add configurable max length for hostnames`
- `fix(spoa): release pooled message on frame decode error`
- `docs: update deployment guidelines for Kubernetes`

---

## Pull Request Guidelines

1. **Branch Naming:** Create a feature branch with a descriptive name:
   ```bash
   git checkout -b feat/custom-latency-buckets
   # or: git checkout -b fix/socket-unlink-cleanup
   ```

2. **Surgical Changes:** Keep PRs small and focused on a single change or fix. Avoid unrelated formatting changes or refactoring adjacent code.

3. **Add Tests:** Every bug fix must include a test reproducing the problem. Every new feature must include accompanying unit tests (and E2E tests if protocol-level).

4. **Verify Locally:** Ensure the following commands pass before pushing:
   ```bash
   make fmt
   make vet
   make test
   make test-race
   ```

5. **Submit PR:** Open a Pull Request against the `main` branch:
   - Provide a concise description of the motivation and changes.
   - Reference any relevant GitHub issues (`Closes #123`).
   - Confirm that all CI checks pass.
