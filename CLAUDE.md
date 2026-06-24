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

Requires Go 1.21+, a C toolchain (CGo on), and Fluent Bit 1.9+.

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
generated `in_beats.h` can be ignored. There are no Go tests in this repo.

## Run

```bash
fluent-bit -e ./in_beats.so -c fluent-bit.conf   # load the .so directly
```

### End-to-end stack

`example/docker-compose.yml` brings up the full loop for manual verification:
`flog` generates logs → `filebeat` ships them via lumberjack → this plugin
(inside the `fluent-bit` image) prints them to stdout. Its build context is the
repo root (`build: ..`), where the plugin and `Dockerfile` live.

```bash
docker compose -f example/docker-compose.yml up --build   # or: cd example && docker compose up --build
```

`Dockerfile` (repo root) runs the same go.mod fixup as above in its build stage,
then copies `in_beats.so`, `plugins.conf`, and `fluent-bit.conf` into the Fluent
Bit image.

## Lifecycle (CGo-exported entry points in main.go)

- `FLBPluginRegister` — registers the plugin name `beats`.
- `FLBPluginInit` — reads config, starts the go-lumber TCP server, launches the
  `consume()` goroutine.
- `FLBPluginInputCallback` — polled on a timer; blocks ~1s for the first record,
  then drains up to `maxBatch` (2000) queued records into one C-allocated
  msgpack buffer.
- `FLBPluginInputCleanupCallback` — deliberately a no-op (see C memory note).
- `FLBPluginExit` — closes `done`, shuts the server, waits for the goroutine.

`consume()` drains go-lumber batches onto the buffered `records` channel and
ACKs each batch.

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
- **ACK timing / durability.** A batch is ACKed once buffered in the `records`
  channel, not after Fluent Bit flushes downstream (the Go API exposes no flush
  hook). A crash with records still buffered loses them despite the Beat having
  seen an ACK. Stronger guarantees need a persistent queue in `consume()`.
- **`ca_file` without `tls_active` is a hard startup error** — it would
  otherwise silently start a plaintext listener with no client-cert
  verification, the opposite of the operator's intent.
- **Record timestamp** comes from the event's `@timestamp` (RFC3339Nano) when
  present, else the receive time (`recordTime`).
