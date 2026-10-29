# Xray v26.10.29 Synchronization Plan

> Execute inline with superpowers:executing-plans and verification-before-completion; request one final independent review.

**Goal:** Merge official Xray v26.10.29 into ilnli/Xray-core and release proxynode with a verified immutable dependency pin.

**Architecture:** Preserve upstream ancestry and wyx2685 extensions through a normal merge. Validate the local merged core before publishing its revision, then test proxynode against that published revision.

**Tech Stack:** Go 1.27.3, GOEXPERIMENT=jsonv2, Xray v26.10.29, current stable Mihomo, GitHub Actions.

**Spec:** User's request to merge the release; AGENTS.md and docs/xray-core-comparison.md define fork and interoperability requirements.

## Constraints and Review Focus

- Retain Xray as the only kernel and preserve AnyTLS/TUIC, accounting and audit integration.
- Confirm current stable Mihomo and checksum; real TCP, inner TLS, UDP/XUDP and Vision traffic must pass the existing matrix.
- Preserve custom fork CI when merging upstream workflow changes.
- Keep dependency versions immutable and downloads/releases within ilnli repositories.
- Do not deploy to production or change existing node configuration.

## Tasks

- [x] Merge official tag v26.10.29 into the clean Xray checkout; inspect conflicts and extension differences. Run affected core/configuration and fork protocol tests and build Xray with Go 1.27.3.
- [x] Build/test proxynode using a temporary local module replacement. Commit/push the validated core, then update proxynode go.mod/go.sum to the published revision.
- [x] Run proxynode full tests, audit/dispatcher race checks, static build and all 31 real Mihomo interoperability cases; validate deployment tests.
- [x] Update README, current compatibility documentation, dependency guidance and release notes for proxynode-v0.2.3. Review changed files once, commit/push, tag and verify CI/release assets.

## Execution Record

- Both checkouts began clean (core d84c1121; proxynode c8ff205). Work in the existing checkouts for this authorized upstream merge; no unrelated application changes.
- Confirmed 2026-10-29 that current stable Mihomo remains v1.19.33. Reuse the checksum-pinned client and unchanged matrix, rerun against the new core.
- Existing tests cover this dependency synchronization; add a regression test only if integration requires a new behavior fix.

- Core merge `3c160b27cb42` retains both ancestors; changed core package tests and standalone build passed. Local adapter build/tests passed before the fork push. The sole workflow conflict kept the existing fork checks; no runtime merge conflicts or fork protocol edits.
- Published module pin verified through `go mod tidy` and binary build metadata. Full proxynode tests, race checks, 18 deployment tests, shell syntax and workflow lint passed. Static release candidate passed 31/31 real Mihomo cases; report: `/tmp/mihomo-261029/report.json`. Official Mihomo archive digest remains `132f6f3edf9a65e62f04853aed3358472e0b2b7666daa8419edfa2ddf5bb6323`.
- Independent final review found no actionable findings; confirmed ancestry, retained extensions, immutable published SHA, static-candidate/report hash and unchanged audit/accounting paths. Remote CI and release artifact checks remain the final publishing gate; no production deployment or untested-combination claims.

- Delivered proxynode commit `74bf9540474afc50261047848e2b3d9368ddb012` and tag `proxynode-v0.2.3`. Core CI [40903585149](https://github.com/ilnli/Xray-core/actions/runs/40903585149), node main CI [40903895737](https://github.com/ilnli/v2node/actions/runs/40903895737) and release CI [40903992368](https://github.com/ilnli/v2node/actions/runs/40903992368) all succeeded. Core CI was manually dispatched after no push run appeared; node main/tag workflows triggered automatically.
- [Release](https://github.com/ilnli/v2node/releases/tag/proxynode-v0.2.3) is published and marked latest with amd64, arm64 and SHA256SUMS assets. The actual amd64 release was downloaded, checksum-validated and installed under `/tmp/proxynode-v023-install` with `--no-service`. Its version and Go build metadata confirm v0.2.3, Go 1.27.3 and the new immutable core; `pnode` links to `proxynode-manager`. No production service or configuration was changed.
