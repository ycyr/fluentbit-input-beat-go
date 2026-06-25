# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Optional WAL (`wal_path` config key) using `go.etcd.io/bbolt` for stronger
  delivery durability: events are persisted to disk before being enqueued, and
  replayed automatically on restart if the plugin crashed with unprocessed
  records.

### Changed
- Batch ACK is now deferred until `collect()` encodes the records into the
  msgpack buffer, reducing the window in which a plugin crash causes event loss
  despite the Beat having already received an ACK.
- CI workflow now triggers on `feature/**` branches in addition to `main`.

### Fixed
- WAL write failures now drop the batch (Beat retries on timeout) instead of
  silently proceeding — preserving the durability contract when `wal_path` is
  set.
- WAL delete errors are now logged; previously they were silently ignored, which
  could cause WAL growth and repeated replay on restart.
- TCP listener is now closed on WAL open failure, preventing a port leak when
  `FLBPluginInit` returns `FLB_ERROR`.

### CI
- Bumped GitHub Actions to latest major versions: `checkout@v7`, `setup-go@v6`,
  `upload-artifact@v7`, `download-artifact@v8`.

## [0.1.0] - 2026-06-24

### Added
- Fluent Bit Go input plugin speaking the Beats/Lumberjack v1 and v2 protocol.
- Accepts batches from Filebeat 5.x, 6.x, 7.x, and 8.x.
- Optional TLS (server-TLS and mTLS via `tls_active`, `cert_file`, `key_file`,
  `ca_file` config keys — avoids Fluent Bit's reserved `tls.*` namespace).
- Configurable listen `address` and `buffer_size`.
- Release workflow publishing `in_beats-linux-amd64.so` and
  `in_beats-linux-arm64.so` as GitHub release assets on `v*` tags.
- Integration test matrix: Filebeat 5/6/7/8 and no-TLS/TLS/mTLS transports.
- Protocol documentation in `docs/`.

[Unreleased]: https://github.com/ycyr/fluentbit-input-beat-go/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/ycyr/fluentbit-input-beat-go/releases/tag/v0.1.0
