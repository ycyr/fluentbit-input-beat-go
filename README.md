# fluentbit-input-beat-go

A **Fluent Bit input plugin** (written in Go) that speaks the **Beats /
Lumberjack protocol** — the wire format Filebeat and the other Elastic Beats
shippers use. It lets you point Filebeat at Fluent Bit instead of
Logstash/Elasticsearch.

It embeds the server side of
[`elastic/go-lumber`](https://github.com/elastic/go-lumber) and bridges decoded
events into Fluent Bit via
[`fluent/fluent-bit-go/input`](https://github.com/fluent/fluent-bit-go).

```
Filebeat ──lumberjack(v1/v2)──▶ [beats input] ──msgpack──▶ Fluent Bit pipeline ──▶ any output
```

## Download

Pre-built shared objects for Linux are attached to each [GitHub Release](https://github.com/ycyr/fluentbit-input-beat-go/releases):

| Asset | Architecture |
|-------|-------------|
| `in_beats-linux-amd64.so` | x86-64 |
| `in_beats-linux-arm64.so` | ARM64 |

Download the `.so` for your platform, then load it in Fluent Bit:

```bash
# plugins.conf
[PLUGINS]
    Path /path/to/in_beats-linux-amd64.so
```

## Build from source

Requires Go 1.26+, a C toolchain (cgo), and Fluent Bit **1.9+** (input plugins
in Go need 1.9 or newer).

```bash
# fluent-bit-go has no tagged releases, and the pseudo-version pinned in
# go.mod may no longer be resolvable through proxy.golang.org. Drop it first,
# then re-fetch master directly from GitHub so `go get`/`go mod tidy` don't
# fail with "invalid version: unknown revision ...".
go mod edit -droprequire=github.com/fluent/fluent-bit-go
GOPROXY=direct go get github.com/fluent/fluent-bit-go@master
go get github.com/elastic/go-lumber@latest
GOPROXY=direct go mod tidy
CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -o in_beats.so .
```

This produces `in_beats.so` (plus a generated `in_beats.h` you can ignore).

## Run

```bash
fluent-bit -c fluent-bit.conf
# or load the shared object directly:
fluent-bit -e ./in_beats.so -c fluent-bit.conf
```

Or with Docker:

```bash
docker build -t fluentbit-input-beat-go .
docker run --rm -p 5044:5044 fluentbit-input-beat-go
```

### End-to-end demo

`example/` contains a self-contained loop — `flog` generates logs, Filebeat
ships them over lumberjack, the plugin prints them to stdout. Run it via the
Makefile (`make help` lists targets):

```bash
make demo        # plaintext
make demo-tls    # mTLS (generates throwaway certs on first run)
make down        # stop both stacks
```

Equivalently, the raw commands:

```bash
docker compose -f example/docker-compose.yml up --build                 # plaintext
example/tls/gen-certs.sh && \
  docker compose -f example/docker-compose.tls.yml up --build            # mTLS
```

The TLS variant reuses the same image, mounting `example/fluent-bit.tls.conf`
and the generated certs over the baked-in config. The server cert's SAN is
`DNS:fluent-bit` (the service name Filebeat dials); `ca_file` makes it mTLS —
drop it plus the client cert in `example/filebeat.tls.yml` for plain server-TLS.

## Configuration

Because Go **input** plugins cannot use Fluent Bit's reserved keys
(`host`, `port`, `listen`, and all `tls.*`), this plugin uses custom keys:

| Key           | Default        | Description                                                          |
|---------------|----------------|----------------------------------------------------------------------|
| `address`     | `0.0.0.0:5044` | Bind address `host:port` for the listener                            |
| `buffer_size` | `16384`        | Internal record channel capacity                                     |
| `enable_v1`   | `false`        | Accept Lumberjack v1 (only needed for Beats older than 5.x)         |
| `enable_v2`   | `true`         | Accept Lumberjack v2 (all modern Beats)                              |
| `tls_active`  | `false`        | Enable TLS                                                           |
| `cert_file`   | —              | PEM server certificate (required when `tls_active`)                  |
| `key_file`    | —              | PEM private key (required when `tls_active`)                         |
| `ca_file`     | —              | PEM CA bundle; with `tls_active`, enables mTLS (`RequireAndVerifyClientCert`). Setting it without `tls_active` is rejected at startup. |
| `wal_path`    | —              | Path to a bbolt WAL file (e.g. `/var/lib/fluent-bit/beats-wal.db`). When set, events are persisted to disk before ACKing, and replayed on restart if the plugin crashed with unprocessed records. Disabled by default. |

```ini
# Plain TCP (Filebeat 7.x / 8.x default)
[INPUT]
    Name         beats
    Tag          beats.*
    address      0.0.0.0:5044

# Server-TLS
[INPUT]
    Name         beats
    Tag          beats.*
    address      0.0.0.0:5044
    tls_active   true
    cert_file    /etc/certs/server.crt
    key_file     /etc/certs/server.key

# mTLS (client cert required)
[INPUT]
    Name         beats
    Tag          beats.*
    address      0.0.0.0:5044
    tls_active   true
    cert_file    /etc/certs/server.crt
    key_file     /etc/certs/server.key
    ca_file      /etc/certs/ca.crt
```

## Point Filebeat at it

```yaml
# filebeat.yml
output.logstash:                 # Beats ships to Logstash-protocol = lumberjack
  hosts: ["fluent-bit-host:5044"]
  # ssl.enabled: true
  # ssl.certificate_authorities: ["/path/ca.crt"]
  # ssl.certificate: "/path/client.crt"   # mTLS only
  # ssl.key: "/path/client.key"           # mTLS only
```

Each Beats event arrives as a record whose timestamp is taken from the event's
`@timestamp` field when present (RFC3339), otherwise the receive time.

## Testing

```bash
make test                # unit tests — no Docker required
make test-integration    # Filebeat 5, 6, 7, 8 version matrix (requires Docker)
make test-transport      # no-TLS, server-TLS, mTLS transport matrix (requires Docker)
```

Unit tests (`main_test.go`) cover config parsing, timestamp extraction (RFC3339
with/without nanoseconds, timezone offsets), cert pool loading, realistic Filebeat
5/6/8 event payloads, `@metadata` passthrough, and the `collect` → msgpack encode
path including the 2048-record batch drain limit.

Integration tests spin up the plugin image against real Filebeat containers.
`test-integration` verifies Lumberjack v2 compatibility across Filebeat 5, 6, 7,
and 8; `test-transport` verifies the three TLS modes (certs are generated fresh per
run, no fixtures committed).

## Protocol documentation

`docs/` contains reference material for the Lumberjack/Beats wire protocol and
Filebeat configuration, sourced from the canonical upstream repositories:

| File | Content |
|------|---------|
| `docs/lumberjack-v1-protocol.md` | Wire format from `elastic/logstash-forwarder` (deprecated, v1 only) |
| `docs/lumberjack-v2-protocol.md` | Derived from `elastic/go-lumber` source (no official v2 spec exists) |
| `docs/plugin-protocol-notes.md` | ACK timing, `FLBTime` encoding, reserved keys, TLS modes |
| `docs/filebeat-ssl-config.md` | Filebeat `ssl.*` YAML fields (`elastic-agent-libs/tlscommon`) |
| `docs/filebeat-output-logstash.md` | Full `output.logstash.*` config and Filebeat event JSON structure |

## Important caveats

- **Single instance per process.** The Go input callback gets no per-instance
  context, so state is package-level. Run one `[INPUT] beats` per Fluent Bit
  process (or extend `FLBPluginInit` with an address-keyed registry).
- **ACK timing / durability.** Batches are ACKed inside `collect()`, after
  events are encoded into the msgpack buffer handed to Fluent Bit. With
  `wal_path` set, events are also persisted to a bbolt WAL before pushing to
  the internal channel, and replayed automatically on restart — closing the gap
  of records in-flight when the plugin crashes. The remaining unaddressable
  window is Fluent Bit crashing after receiving the buffer but before writing to
  an output (the Go API has no flush-confirmation hook).
- **Not compiled in this environment.** Build it yourself with the commands
  above; pin `go.mod` versions via `go mod tidy`.

## License

Apache-2.0 (matches both upstream libraries).

---

*This project was vibe coded with [Claude Code](https://claude.ai/code).*
