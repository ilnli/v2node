# Multi-node Script Management Plan

> Execute with superpowers:executing-plans; verify the configuration mutation path with tests before implementation.

**Goal:** Manage multiple panel v2node IDs in one proxynode configuration without manual JSON edits or upstream telemetry/advertising.

**Design:** Extend the existing standalone Bash manager with Python standard-library JSON handling (already an installer dependency). Commands are `add-node`, `remove-node`, `list-nodes`; add supports comma-separated/repeated IDs and interactive hidden key entry. Keep manager self-contained so release packaging remains compatible. Installer accepts the same multi-ID syntax for first-time setup.

**Constraints:** Preserve Log/Audit/unknown fields and existing node settings; reject duplicate host+ID additions and ambiguous removals; validate before writing; serialize mutations with a file lock, retain one 0600 backup and atomically replace the configuration. Do not call panel APIs during configuration edits. Service restart remains explicit. No kernel or dependency change.

**Privacy scope:** Original upstream installer calls `https://api.v-50.me/counter`; current fork installer already omits that request. Keep all upstream telemetry and advertising absent from shipped scripts/output. Preserve license/source attribution and node traffic/online/audit reporting to the configured panel.

## Tasks

- [x] Add failing behavioral tests for batch add/list/remove, preservation/backup, duplicate and ambiguous rejection, invalid input, no manager networking, installer multi-ID handling and exact own-release download requests.
- [x] Implement manager commands, interactive prompts/help, atomic JSON mutation and installer multiple IDs. Run deployment suite and Bash syntax checks.
- [ ] Document commands/privacy scope and publish v0.2.1 notes, review new code once, push and run own CI/release. Verify actual packaged manager in a staging installation.

## Decisions

Use commands with optional prompts rather than a new menu framework. Persist the last backup only. Existing installations keep config unchanged during install/update; adding nodes uses the manager. `--config` selects a file for manager operations/testing; authentication keys never appear in listing output.

- Verification: feature tests failed before implementation; 14 deployment tests now pass, including concurrent writes and blank initial setup. Shell syntax and diff checks pass. No kernel or dependency modifications.
- Independent focused review found no actionable issues. Real TTY add of three IDs verified hidden key input; list output contains no key.
