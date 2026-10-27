# Fork Release Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans for inline execution.

**Goal:** Publish the audited node to ilnli/v2node and make its releases installable from that fork.

**Architecture:** Tag-driven release workflow builds two Linux archives after tests. Installer consumes only fork releases, checks checksums, preserves configuration and integrates with systemd/OpenRC.

**Tech Stack:** Git, GitHub Actions, Go, Bash, Python standard-library tests.

**Spec:** `docs/superpowers/specs/2026-10-27-fork-release-design.md`

## Global Constraints

- Preserve remote history; no force push, no production installation.
- Binary/script downloads point to ilnli/v2node; no upstream fallback.
- Keep existing config and audit files; require explicit legacy migration.
- Release permissions limited to publishing job; build/test jobs read-only.

## Review Focus

Checksum failure must leave old installation intact; new install must not enable audit against an unready panel; no legacy service stop without migration flag; tag/asset naming must match installer; releases must belong to ilnli/v2node.

### Task 1: Installer and management commands

Files: `script/install.sh`, `script/proxynode.sh`, `script/v2node.sh`, `tests/deployment/test_install.py`.
- [x] Write failing installer behavior tests using fixture downloads and temporary installation roots.
- [x] Implement fork-only download, checksum verification, config preservation and service/migration handling.
- [x] Run Python tests and shell syntax/lint checks.

### Task 2: Package and release workflow

Files: `script/package.sh`, `script/download-geodata.sh`, `.github/workflows/{test,release}.yml`, README and deployment documentation.
- [x] Build static amd64/arm64 archives with consistent asset names and checksum manifest (both GitHub matrix builds passed).
- [x] Configure tag-triggered tests/build/release; minimal permissions and no upstream publication.
- [x] Validate workflow/shell, full Go tests and local amd64 package/checksum/staged execution. ARM64 verification will run in CI.

### Task 3: Publish and verify

- [x] Push main without force, tag proxynode-v0.1.0, monitor fork CI: both workflows succeeded.
- [x] Confirm both release assets, checksum download and staged installer against real release.
- [x] Record results; no CI fixes were required. See docs/verification.md.
