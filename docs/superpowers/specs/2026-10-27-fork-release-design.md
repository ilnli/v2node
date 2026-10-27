# Fork releases and deployment

User authorized pushing proxynode to `ilnli/v2node`, automated releases in that fork, and deployment scripts that fetch our binaries rather than upstream binaries.

## Decisions

- Remote `main` is exactly the original source snapshot. Preserve its history by joining it to the already verified local history, keeping the local file tree, then fast-forward push to remote main. No force push.
- Main/PR CI tests Go and installer behavior. `proxynode-vX.Y.Z` tags trigger tests, static Linux amd64/arm64 packages and a GitHub Release in `ilnli/v2node`. Initial tag: `proxynode-v0.1.0`.
- Packages include binary, config sample, management/install scripts, license and geodata. Geodata comes from a pinned Loyalsoldier rules release with checksum validation; installation downloads everything from our fork's release.
- Installer checks SHA-256 before extraction/replacement, preserves existing configuration and audit cache, supports systemd/OpenRC, and permits staging via `--prefix ... --no-service`.
- Legacy migration is explicit (`--migrate-v2node`): copy legacy config if needed and stop/disable legacy service before starting proxynode. Normal installations do not alter old services.
- New configurations disable audit until the panel endpoint is ready. Existing audit settings are preserved on upgrades.

## Validation

Run installer tests in temporary directories with controlled download fixtures: own-repository URLs, supported architectures, checksum failure leaves binary/config unchanged, configuration preservation, default audit disabled, and migration service ordering. Validate shell/workflow syntax, full Go tests, package/build, then monitor the actual fork Actions run and verify published assets and checksums.
