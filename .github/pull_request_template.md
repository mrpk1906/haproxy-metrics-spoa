## Description

<!-- Provide a concise summary of the changes in this pull request and the motivation behind them. -->

## Related Issue

<!-- Link the related issue if applicable: Fixes #123 / Closes #123 / Relates to #123 -->
Fixes #

## Type of Change

<!-- Please mark the relevant option with an [x]. -->

- [ ] `feat`: A new feature
- [ ] `fix`: A bug fix
- [ ] `docs`: Documentation updates or additions
- [ ] `refactor`: Code refactoring without changing functionality
- [ ] `perf`: A code change that improves performance
- [ ] `test`: Adding or correcting tests
- [ ] `chore`: Tooling, build system, or dependency updates

## Verification Checklist

<!-- Please verify the following checks pass before requesting a review. -->

- [ ] All unit tests pass: `make test`
- [ ] Race detection tests pass: `make test-race`
- [ ] End-to-end tests pass (if applicable): `make test-e2e`
- [ ] Code formatting and vetting verified: `make fmt` and `make vet`
- [ ] Documentation updated (`README.md`, examples, doc comments) if applicable

## Architectural Invariants Checklist

- [ ] **Pure Go:** Zero CGo dependencies (`CGO_ENABLED=0`)
- [ ] **Zero Latency Overhead:** Non-blocking SPOP message handling (no slow I/O or blocking locks)
- [ ] **Cardinality Protection:** All virtual hostnames pass through `normalizer.Guard`
- [ ] **Commit Hygiene:** Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/) format
