# GitHub Copilot instructions

## What this is

A single-file Fluent Bit **input** plugin (`main.go`) that speaks the Beats /
Lumberjack protocol. It accepts batches from Filebeat (and other Elastic Beats),
decodes them from JSON (v2) or key-value (v1), and feeds each event into the
Fluent Bit pipeline as a MessagePack record.

```
Filebeat --lumberjack(v1/v2)--> [beats input] --msgpack--> Fluent Bit pipeline
```

## Build

```bash
go mod edit -droprequire=github.com/fluent/fluent-bit-go
GOPROXY=direct go get github.com/fluent/fluent-bit-go@master
go get github.com/elastic/go-lumber@latest
GOPROXY=direct go mod tidy
CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -o in_beats.so .
```

## Test

```bash
CGO_ENABLED=1 go test -v ./...                          # unit tests
CGO_ENABLED=1 go test -v -tags integration -run 'TestFilebeat' -timeout 15m ./...
CGO_ENABLED=1 go test -v -tags integration -run 'TestTransport' -timeout 10m ./...
```

## Key constraints — read before editing

- **CGo in test files is forbidden.** Go forbids `import "C"` in `_test.go`.
  Testable logic lives in pure-Go helpers (`parseBool`, `parseInt`, `collect`,
  `recordTime`, `loadCertPool`) extracted from the CGo boundary.

- **Reserved Fluent Bit config keys.** The engine overwrites `host`, `port`,
  `listen`, and all `tls.*` on input plugins. This plugin uses `address`,
  `tls_active`, `cert_file`, `key_file`, `ca_file` instead. Do not reintroduce
  the reserved names.

- **Single instance per process.** `FLBPluginInputCallback` receives no
  per-instance context, so all state lives in package-level `var gCtx`. Only
  one `[INPUT] beats` section per Fluent Bit process is supported.

- **C memory ownership.** The buffer returned from `FLBPluginInputCallback` must
  be C-allocated (`C.CBytes`). Fluent Bit owns and frees it — do NOT free it in
  `FLBPluginInputCleanupCallback` (double-free).

- **ACK timing.** Batches are ACKed inside `collect()`, after events are encoded
  into the msgpack buffer handed to Fluent Bit — not merely after buffering in
  the channel. `record.ack` is non-nil only on the last event of each batch;
  `collect()` calls it after `appendRecord`. Remaining window: callback return →
  Fluent Bit output flush (Go API has no confirmation hook).

- **`ca_file` without `tls_active` is a hard startup error** — it would silently
  start a plaintext listener, opposite of the operator's intent.

## File map

| File | Role |
|------|------|
| `main.go` | Entire plugin — CGo exports, server lifecycle, msgpack encoding |
| `main_test.go` | Unit tests (no CGo) |
| `integration_test.go` | Filebeat version matrix (5/6/7/8), build tag `integration` |
| `tls_integration_test.go` | TLS transport matrix (no-TLS/TLS/mTLS), build tag `integration` |
| `example/integration/` | Compose stacks and configs for integration tests |
| `docs/` | Protocol and Filebeat configuration reference |
| `.github/workflows/ci.yml` | Unit + integration tests on push/PR |
| `.github/workflows/release.yml` | Builds `.so` assets and publishes GitHub release on `v*` tags |

## Protocol summary

Lumberjack v1: frames `W` (window) / `D` (data, key-value) / `C` (compressed) / `A` (ack).
Lumberjack v2: same framing, `J` (JSON) replaces `D`. All modern Beats (5.x+) use v2.
See `docs/lumberjack-v2-protocol.md` for the full wire format.
