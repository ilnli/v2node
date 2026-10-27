# Proxynode connection audit design

Create an independent local fork of `/root/projects/v2node`, preserving its license and upstream Go module path for compatibility. No changes to the source project or ProxyBoard in this phase.

## Agreed behavior

Record authenticated user ID, observation time (UTC), node ID, transport, requested destination and port, and domain when available from the destination or existing HTTP/TLS/QUIC sniffing. These are connection attempts, not HTTP page views, confirmed connections, or decrypted URL paths.

Use memory first and spill backlog to disk. Audit-owned buffered data and bounded working buffers remain below 256 MiB, shared across all configured nodes; this is not a cap on Xray or total process RSS. Limit spool files to 1 GiB. Delete acknowledged batches promptly. **When capacity is exhausted, drop newly arriving records; preserve older queued records.** This supersedes the earlier oldest-first proposal.

## Implementation decisions

One process-wide collector with bounded ingestion and one spool worker keeps disk/network work off the dispatcher. Immutable batches have stable IDs and retain their node destination. A separate sender drains batches, retries failures, and acknowledges only an explicit matching success response. Default memory spool budget 64 MiB, leaving ample room for ingestion, metadata, serialization and HTTP buffers. Batch files and queue metadata are bounded. Whole files are deleted on acknowledgment, never rewritten at the front. Existing memory batches precede disk batches; new batches may use memory again only once the disk backlog clears.

Spool files recover on startup. Memory is flushed on graceful stop/reload; abrupt process death can lose memory-only records. Disk write failures drop the new batch and log a counter; proxy traffic continues. HTTP sends have finite timeouts and response limits. Acknowledgment loss may cause duplicate delivery: the panel must deduplicate by batch ID transactionally. Removed panel/node targets remain pending until restored, within the same global limits.

## Integration

Hook both dispatcher entry paths after sniffing and before routing. Preserve original destination independently of sniffed domain. Resolve user ID at collection time. Reuse node authentication on `POST /api/v1/server/UniProxy/audit`. Keep `node_type=v2node` for panel compatibility. Opt-in top-level Audit configuration; use a dedicated persistent directory. Existing panels continue to function with audit disabled.

## Verification

Exercise memory-to-disk transition, newest-drop behavior, successful cleanup, restart recovery, retry identity, rejected/mismatched acknowledgments, concurrency, user/node attribution, domain fallback, and both dispatcher entry paths. Run targeted race tests, full Go tests and a production build. Use synthetic local HTTP endpoints only.
