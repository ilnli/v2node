# Execution ledger — 2026-10-27-audit.md

- Source copied with git archive; source working tree untouched, no upstream Git metadata copied.
- Latest user instruction supersedes oldest-first eviction: drop newest on saturation.
- User authorized implementation; execute inline in the requested independent proxynode directory.
- Global constraints and integration interfaces checked; one shared queue owns all limits.

- Task 1: implemented and verified — memory/disk FIFO, disk reservations, newest-drop, cleanup, recovery, exclusive spool ownership; stdlib audit race tests pass.
- Task 2: implemented and verified — bounded HTTP transport, authentication, stable batch IDs and complete ACK matching; HTTP response scenarios pass.
- Ruling: place the audit-only HTTP sender in `common/audit/http.go`, not the existing Resty client — bounded response handling, sanitized errors, and standard-library-only tests can be exercised without upstream dependencies; wire authentication remains unchanged.
- Task 3: implementation complete — both dispatcher hooks, user/node attribution, configuration, graceful stop/reload and partial-start cleanup. Integration tests written; execution blocked by missing external dependencies and sandbox networking.
- Ruling: user requested a new directory, so independent Git repository at the requested `proxynode/` location supplies isolation; retain upstream module path and license.
- Ruling: disk reservations include memory batches so graceful flush cannot exceed 1 GiB. Maximum memory spool 128 MiB leaves headroom inside the 256 MiB audit working-buffer budget; no claim of a process RSS cap.
- Independent review: no outstanding important findings after partial-start cleanup guard; audit race suite passes.
- Final verification: 9 top-level tests including 7 HTTP subcases pass under race detection; 58 Go files parse; diff check passes. Full Go test/build commands cannot resolve missing dependencies. Details: `docs/verification.md`.
- User explicitly authorized dependency download/update. Retry failed due socket permission; escalated `go get` was automatically rejected by environment policy. Existing dependency versions remain unchanged.
- No source-project or ProxyBoard changes, no remote repository, deployment or publishing.

- Resumed verification: environment changed to auto-review; authorized network escalation approved. Default proxy had EOF/TLS connection errors; goproxy.cn succeeded with Go checksum verification retained.
- Dependency check: upgraded only golang.org/x/sys v0.48.0 -> v0.49.0; other queried direct dependencies were current; modified Xray version unchanged.
- Task 3: complete — final `go test -p 4 -count=1 ./...` and `go test -p 4 -race -count=1 ./common/audit ./core/app/dispatcher` passed with GOEXPERIMENT=jsonv2.
- Production build: `build_assets/proxynode` built successfully; executable prints `proxynode 0.1.0-audit`. Earlier dependency/network verification blockers are resolved. No deployment or panel changes.
