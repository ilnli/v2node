# Source and attribution

Proxynode is derived from the local v2node repository at `/root/projects/v2node`, commit `cf513e1f0ba5f22eef6b40e65c22e3e9856d86b2` (2026-10-27 import).

The upstream project identifies itself as `github.com/wyx2685/v2node`, based on a modified Xray core. Its license is retained verbatim in `LICENSE`. Go module/import paths are preserved to retain compatibility with generated code and dependencies; they do not imply this fork is published upstream.

This fork adds bounded connection auditing, disk spooling, authenticated batch delivery, graceful audit shutdown, configuration, tests, and documentation. It uses the existing dependency versions without contacting production services.

Upstream download/update scripts and publishing workflows are removed from this fork to avoid installing or publishing the original binary under the new project name. Build locally using this repository's README and Dockerfile.
