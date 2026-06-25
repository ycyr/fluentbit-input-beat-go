# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A single-file Fluent Bit **input** plugin (`main.go`) that speaks the Beats /
Lumberjack protocol. It embeds the server side of `elastic/go-lumber`, accepts
batches from Filebeat (and other Elastic Beats), and feeds each event into the
Fluent Bit pipeline as a MessagePack record.

```
Filebeat --lumberjack(v1/v2)--> [beats input] --msgpack--> Fluent Bit pipeline --> any output
```

This repo is one of several sibling projects under `plugin-beats/`; the two Go
dependencies (`github.com/fluent/fluent-bit-go`, `github.com/elastic/go-lumber`)
are upstream forks living in adjacent directories. The workspace-level
`../CLAUDE.md` documents that layout.

## Build

Requires Go 1.26+, a C toolchain (CGo on), and Fluent Bit 1.9+.

```bash
# fluent-bit-go has no tagged releases; the pseudo-version pinned in go.mod is a
# master commit that may stop resolving via proxy.golang.org ("invalid version:
# unknown revision ..."). Drop it and re-fetch master directly before building.
go mod edit -droprequire=github.com/fluent/fluent-bit-go
GOPROXY=direct go get github.com/fluent/fluent-bit-go@master
go get github.com/elastic/go-lumber@latest
GOPROXY=direct go mod tidy
CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -o in_beats.so .
```

`buildmode=c-shared` is required (the plugin loads as `in_beats.so`); the
generated `in_beats.h` can be ignored.

## Tests

```bash
make test                # unit tests, no Docker
make test-integration    # Filebeat 5 / 6 / 7 / 8 version matrix (requires Docker)
make test-transport      # no-TLS / TLS / mTLS transport matrix (requires Docker)
```

**Unit tests** (`main_test.go`) — pure Go, no cgo in the test file (Go forbids
`import "C"` in `_test.go`). The testable logic was extracted from the cgo
boundary: `parseBool`/`parseInt` from `cfgBool`/`cfgInt`, and `collect()` from
`FLBPluginInputCallback`. Tests cover:
- `parseBool` / `parseInt` edge cases
- `recordTime`: RFC3339 with/without nanoseconds, timezone offsets, fallback cases
- `loadCertPool`: valid CA, non-PEM, missing file
- `collect` end-to-end with realistic Filebeat 5.x/6.x/8.x event payloads
- `@metadata` passthrough verification
- Batch drain limit (exactly 2048 records per `collect()` call)
- ACK delay: fires inside `collect()` after encoding, not before
- Empty queue blocking, shutdown unblocking, nil context

Decodes real msgpack output using `github.com/ugorji/go/codec` (transitive dep)
with a `flbTimeExt` decoder matching the 8-byte BE sec+nsec wire format.

**Integration tests** (`integration_test.go`, `tls_integration_test.go`) — build
tag `integration`. `TestMain` builds the plugin image once; all subtests run in
parallel with isolated compose projects (unique `-p` names, no host-port
conflicts). Certs for the TLS tests are generated fresh per subtest in
`t.TempDir()` using stdlib `crypto/x509` — no committed fixtures.

Version matrix: Filebeat 5.6.16, 6.8.23, 7.17.25, 8.13.4. Note: Filebeat 5.x
uses `-strict.perms=false` (single-dash flag syntax); 6.x+ accepts `--`.

## Run

```bash
fluent-bit -e ./in_beats.so -c fluent-bit.conf   # load the .so directly
```

### End-to-end demo

`make help` lists all targets. The demo stacks bring up `flog → filebeat → plugin → stdout`:

```bash
make demo        # plaintext (example/docker-compose.yml)
make demo-tls    # mTLS, generates certs on first run (example/docker-compose.tls.yml)
make down        # stop both stacks
```

`Dockerfile` (repo root) runs the go.mod fixup in its build stage, then copies
`in_beats.so`, `plugins.conf`, and `fluent-bit.conf` into the Fluent Bit image.
The TLS compose reuses the same image, mounting `example/fluent-bit.tls.conf` +
certs over the baked-in config (`build: ..`). The server cert SAN is
`DNS:fluent-bit`; `ca_file` makes it mTLS — drop it for plain server-TLS.

## Lifecycle (CGo-exported entry points in main.go)

- `FLBPluginRegister` — registers the plugin name `beats`.
- `FLBPluginInit` — reads config, starts the go-lumber TCP server, launches the
  `consume()` goroutine.
- `FLBPluginInputCallback` — polled on a timer; blocks ~1s for the first record,
  then drains up to `maxBatch` (2048, matching Filebeat's `bulk_max_size` default)
  queued records into one C-allocated msgpack buffer. One `FLBEncoder` is
  allocated per callback invocation and reused across all records in the batch.
- `FLBPluginInputCleanupCallback` — deliberately a no-op (see C memory note).
- `FLBPluginExit` — closes `done`, shuts the server, waits for the goroutine.

`consume()` drains go-lumber batches onto the buffered `records` channel,
attaching each batch's ACK to the last event. `collect()` calls that ACK after
encoding the event into the msgpack buffer, so the Beat is ACKed only after
Fluent Bit has received the data.

## Non-obvious constraints (change these carefully)

- **Single instance per process.** The input callback receives no per-instance
  context, so all state lives in the package-level `var gCtx`. Only one
  `[INPUT] beats` section per Fluent Bit process works. Multiple listeners
  require multiple processes, or reworking `gCtx` into an address-keyed
  registry.
- **Reserved config keys.** Fluent Bit overwrites `host`, `port`, `listen`, and
  all `tls.*` on input plugins. This plugin therefore uses `address`,
  `tls_active`, `cert_file`, `key_file`, `ca_file` instead. Do not reintroduce
  the reserved names. (Full key reference: README.md.)
- **C memory ownership.** The buffer returned from `FLBPluginInputCallback` must
  be C-allocated (`C.CBytes`); Fluent Bit core owns and frees it. Do **not**
  free it in `FLBPluginInputCleanupCallback` — that would double-free.
- **ACK timing / durability.** A batch is ACKed inside `collect()`, after its
  events are encoded into the msgpack buffer and handed to Fluent Bit. With
  `wal_path` configured, events are written to a bbolt WAL (one `db.Update()`
  per batch) before pushing to the channel, and deleted when the ACK fires.
  On startup, `replayWAL()` runs concurrently with the server (as a goroutine)
  and pushes undeleted entries into the channel before `consume()` processes new
  batches. `record.ack` is non-nil only on the last event of each batch — do not
  change that invariant. Remaining gap: Fluent Bit crash after receiving the
  buffer (Go API has no flush-confirmation hook).
- **`ca_file` without `tls_active` is a hard startup error** — it would
  otherwise silently start a plaintext listener with no client-cert
  verification, the opposite of the operator's intent.
- **Record timestamp** comes from the event's `@timestamp` (RFC3339Nano) when
  present, else the receive time (`recordTime`).
