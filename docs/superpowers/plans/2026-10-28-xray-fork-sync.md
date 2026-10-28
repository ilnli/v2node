# Xray Fork Synchronization Implementation Plan

> Execute with superpowers:executing-plans; independent interoperability work uses superpowers:dispatching-parallel-agents.

**Goal:** Maintain ilnli/Xray-core from wyx2685, synchronize official v26.10.21 and adopt it in proxynode with verified Mihomo compatibility.

**Architecture:** Preserve Git ancestry and fork protocols through a normal upstream merge. Pin a published immutable fork revision in proxynode. Test real client/server traffic using a local mock panel and real Mihomo processes.

**Tech Stack:** Go 1.27.3, Xray-core v26.10.21, Mihomo v1.19.33, Python integration harness, GitHub Actions.

**Spec:** ../../xray-core-comparison.md; repository AGENTS.md defines compatibility gates.

## Constraints

Xray is the only kernel. Preserve AnyTLS/TUIC packages, license and upstream history. Keep existing audit limits and drop-newest behavior. User authorizes forks, dependency downloads, pushes and own releases. No production service deployment.

## Tasks

- [x] Fork wyx2685/Xray-core to ilnli/Xray-core and merge official latest release, resolve only actual conflicts, retain extensions. Validate relevant changed core packages and compile proxynode against local checkout before publishing pin.
- [x] Add reproducible real Mihomo VLESS integration matrix under tests/interop covering TCP/TLS/REALITY/Vision, WS/gRPC/HTTPUpgrade/XHTTP, Encryption and UDP/XUDP; verify payloads in both directions and record unsupported cases separately.
- [x] Publish synchronized fork revision, pin go.mod/go.sum, align Go 1.27.3 build tooling; run proxynode tests/race/build and interoperability. Add CI gate before release.
- [ ] Review new changes once, update comparison/verification docs, push fork and proxynode; publish new own release after passing gates.

## Review Focus

Preserve fork-only protocols through upstream changes; Vision must transfer TLS data on Go 1.27.3; panel configuration must actually reach running proxynode; UDP must traverse proxy rather than direct routing; failed client configuration must not be misreported as a pass.

## Execution Notes

- Scope decision: synchronize latest official release, not unreleased main; latest release verified 2026-10-28.
- Parallel work ownership: parent owns Xray merge/dependency/CI; helper owns interoperability harness/docs.

- Core synchronized without conflicts and pushed as d84c11216aba; official and wyx commits are both ancestors. Targeted tests and standalone core/proxynode builds passed. Core CI https://github.com/ilnli/Xray-core/actions/runs/40703985011 passed.
- Integration requirement: new REALITY demands Mihomo `reality-opts.support-x25519mlkem768: true`; preserve upstream protocol behavior and document migration, rather than weakening the handshake.
- Adapter gap found: add optional `tls_settings.alpn` mapping for XHTTP H3. `TestPanelTLSALPNReachesInbound/xhttp_h3` failed before mapping and passed after; unspecified ALPN remains unchanged.

- Final local static-binary interoperability matrix passed 31/31; full Go/race tests, deployment tests and workflow checks passed. Independent final review found no actionable issues.
