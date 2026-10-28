# Pnode Command Entry Plan

**Goal:** Replace the cumbersome daily command with user-selected `pnode`, retaining compatibility.

**Design:** Install a relative `pnode -> proxynode-manager` symlink. Existing commands remain valid; add/list/del are short aliases. Plain `pnode` opens a Chinese numbered menu in a terminal and prints help in noninteractive use. Positional node IDs support `pnode add 101,102` and `pnode del 102`. The menu reuses existing commands and accepts `menu --config FILE` for alternate configurations. No kernel, dependency, or JSON mutation redesign.

- [x] Add focused behavioral checks for alias operations, menu interaction, installed symlink and conflicting pre-existing commands.
- [x] Implement aliases/menu and installer entry; update user-facing help/docs; run deployment tests and shell checks.
- [x] Review this diff once, push and publish v0.2.2 through existing CI; validate released pnode entry in staging.

**Constraints:** Preserve long command compatibility, configuration backups and no-telemetry behavior. Do not overwrite unrelated existing pnode files. No production deployment or service changes from this development session.

Verification: four new behavior tests failed before implementation; all 18 deployment tests now pass, including real PTY menu list/exit and conflicting command protection. Bash syntax and diff checks passed.

Independent review found no actionable issues. Published v0.2.2; main CI 40716636550 and release CI 40716654553 passed. Staged released pnode link, short commands and menu were verified.
