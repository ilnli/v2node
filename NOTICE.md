# Source and attribution

Proxynode is derived from the local v2node repository at `/root/projects/v2node`, commit `cf513e1f0ba5f22eef6b40e65c22e3e9856d86b2` (2026-10-27 import).

The upstream project identifies itself as `github.com/wyx2685/v2node`, based on a modified Xray core. Its license is retained verbatim in `LICENSE`. Go module/import paths are preserved to retain compatibility with generated code and dependencies; they do not imply this fork is published upstream.

This fork adds bounded connection auditing, disk spooling, authenticated batch delivery, graceful audit shutdown, configuration, tests, and documentation. It retains the modified Xray dependency; other dependency updates are recorded in Git and the verification document. No production services were contacted.

Installation/update scripts and release workflows target `ilnli/v2node` exclusively for binaries. Linux packages also include routing datasets from `Loyalsoldier/v2ray-rules-dat`; `GEODATA_SOURCE.txt` identifies the pinned dataset release. Upstream project and dataset attribution are retained.
