# Aletheia API — Project Guide

Backend project focused on Go services, gRPC, and Solidity smart contracts. The rules below consolidate previous `.cursor/rules/*.mdc` guidance.

## General Backend Expertise (applies broadly)

Act as a backend engineer with expertise across database management (SQL/NoSQL/NewSQL), API development (REST/GraphQL/gRPC), server-side Go, performance optimization, scalability, security, caching, data modeling, microservices, testing, logging, containers/orchestration, CI/CD, Kafka/RabbitMQ/Redis, and cloud (AWS/GCP/Azure).

When responding: give clear, concise explanations; offer practical best practices; share code when useful; explain trade-offs; consider scalability, performance, and security; reference official docs when relevant.

## Commit Messages — Conventional Commits (always)

Format:

```
<type>[optional scope]: <description>

[optional body]

[optional footer(s)]
```

- `fix:` — bug fix (PATCH)
- `feat:` — new feature (MINOR)
- Breaking change: append `!` after type/scope, or use `BREAKING CHANGE:` footer (MAJOR)
- Other allowed types: `build`, `chore`, `ci`, `docs`, `style`, `refactor`, `perf`, `test`
- Scope is a noun in parens describing the codebase section, e.g. `feat(parser): add array parsing`
- Description follows `: ` and is a short summary; body (if any) starts one blank line after; footers one blank line after body
- `BREAKING CHANGE` must be uppercase when used as a footer token

## Clean Architecture (Go files)

Follow Clean Architecture: dependencies point inward; business logic independent of frameworks, DBs, UI; keep business logic testable without infrastructure wiring.

Layers:
1. **Domain/Entity** — business entities and core rules; pure Go, no external deps, no framework/transport types.
2. **Application/Use Case** — application workflows; orchestrates domain; depends only on domain; defines interfaces (ports) for repos and external services.
3. **Interface/Adapter** — HTTP/gRPC/CLI adapters; implements application-layer interfaces; translates transport DTOs to/from use-case inputs.
4. **Infrastructure** — repo/integration implementations; keeps persistence details out of domain and use cases.

Use dependency injection (constructor injection, interfaces not concrete types).

Suggested layout:
```
project/
├── cmd/                  # entrypoints and wiring
├── internal/domain/      # entities and core rules
├── internal/usecase/     # application workflows and ports
├── internal/handler/     # HTTP/gRPC adapters
└── internal/repository/  # infrastructure implementations
```

DO: keep business logic in domain/application; use interfaces to decouple layers; keep domain entities pure (no DB/HTTP tags); use DTOs only at boundaries; convert domain errors to transport errors at the adapter layer.

DON'T: let domain depend on external packages (beyond stdlib); put business logic in handlers or repositories; import infrastructure in domain/application; use framework types in domain entities.

## Tests are Mandatory (Go files)

Always write or update tests alongside code changes:
- Cover new features, bug fixes, and behavior-changing refactors.
- Cover happy paths, validation failures, and key edge cases.
- If modifying untested code, add the missing tests in the same task.
- Use `testing` package; keep tests in `_test.go`.
- Prefer table-driven tests; use `t.Run()` and `t.Helper()`.
- Mock external deps (DB, network, FS) for unit tests.
- Run `go test ./...` before finalizing. Don't consider work complete until tests pass.

## Go API development (`**/*_api.go`)

- Use latest stable Go (1.22+); `net/http` + ServeMux (Go 1.22).
- Plan endpoint structure and data flow before coding.
- Proper error handling, including custom error types when useful.
- Correct status codes, JSON formatting, input validation.
- Use concurrency when it benefits API performance.
- Follow RESTful design; include required imports/setup.
- Use stdlib `log` or a simple custom logger.
- Consider middleware for cross-cutting concerns (logging, auth).
- Implement rate limiting and auth when appropriate.
- No todos/placeholders; brief comments only for complex logic.
- If unsure on a best practice, say so rather than guess.
- Suggest tests using Go's `testing` package.

## gRPC services (`**/grpc/**/*.go`)

- Define proto messages and services; generate Go via `protoc`.
- Implement server with proper request/response handling.
- Proper error handling, validation, and gRPC status codes.
- For DB interactions follow the database rules below.
- Consider interceptors for logging, auth, tracing.
- Follow gRPC best practices for streaming, deadlines, cancellation.

## Protocol Buffer files (`**/*.proto`)

- Messages in PascalCase, fields in camelCase; descriptive names.
- Set `go_package` correctly.
- Reserve field numbers 1–15 for frequent fields.
- Never reuse field numbers; `reserved N, M to K;` and `reserved "old_name";` for removals.
- Hierarchical package names with version suffix (e.g. `com.company.service.v1`).
- Organize imports; remove unused ones.
- Design service methods with clear request/response types; pick appropriate streaming type; document with comments; consider idempotency and side effects.
- Never change field numbers; use optional for new additions; mark deprecated with `deprecated` + migration note.
- Keep messages focused; avoid deep nesting; use enums over strings/ints for fixed sets; use `oneof` for mutually exclusive fields; prefer `google.protobuf.Timestamp` for times.

## Database Interactions (`**/db/**/*.go`)

- Use prepared statements to prevent SQL injection.
- Handle DB errors gracefully.
- Consider an ORM for complex queries/data modeling.
- Close connections when no longer needed.
- Use connection pooling for performance.

## Solidity Security & Consistency (`**/*.sol`)

Prioritize security and state consistency over feature speed.

Core security:
- Checks-Effects-Interactions to reduce reentrancy risk.
- Minimize external calls; treat external contracts as untrusted.
- Validate inputs and critical assumptions (`require`/custom errors).
- Strict access control (`onlyOwner`/role-based) for privileged functions.
- Pausability/emergency stop for sensitive state-changing functions when relevant.
- Do not use `tx.origin` for authorization.
- Emit events for critical state changes and privileged actions.

Consistency & correctness:
- Define and preserve explicit invariants (supply totals, balance conservation).
- Update state atomically; avoid partial writes before possible failure points.
- Deterministic accounting; no hidden side effects.
- Prefer pull-over-push payments.
- Guard against replay/double-execution.
- Use `^0.8.x` safety checks; avoid unnecessary `unchecked`.

External interaction:
- Use OpenZeppelin audited primitives (ERC standards, access control, guards).
- Handle ERC20 defensively (non-standard returns, fee-on-transfer).
- Explicit trust boundaries for oracles, bridges, admin deps.
- Slippage/deadline checks for value-sensitive operations.

Upgradeability & storage:
- Choose upgradeability intentionally; if using proxies, pick a pattern (UUPS/Transparent) and be consistent.
- Never reorder or remove storage variables in upgradeable contracts.
- Reserve storage gaps in upgradeable base contracts.
- Timelocks or multisig for upgrade/admin paths where appropriate.

Testing & verification:
- Unit tests for happy paths, failure paths, permission boundaries.
- Invariant/property tests for critical accounting and authorization.
- Fuzz tests for edge conditions.
- Run static analysis (e.g. Slither) on every meaningful change.
- High-severity findings are release blockers.

Delivery gate: don't mark contract work complete without tests and security checks passing. If a gas vs safety trade-off is made, document the rationale explicitly.
