# fluent-bit-beats

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

## Build

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
docker build -t fluent-bit-beats .
docker run --rm -p 5044:5044 fluent-bit-beats
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
| `enable_v1`   | `false`        | Accept Lumberjack v1 (only needed for Beats older than 7.x)         |
| `enable_v2`   | `true`         | Accept Lumberjack v2 (all modern Beats)                              |
| `tls_active`  | `false`        | Enable TLS                                                           |
| `cert_file`   | —              | PEM server certificate (required when `tls_active`)                  |
| `key_file`    | —              | PEM private key (required when `tls_active`)                         |
| `ca_file`     | —              | PEM CA bundle; with `tls_active`, enables mTLS (`RequireAndVerifyClientCert`). Setting it without `tls_active` is rejected at startup. |

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
make test-integration    # Filebeat 6, 7, 8 version matrix (requires Docker)
make test-transport      # no-TLS, server-TLS, mTLS transport matrix (requires Docker)
```

Unit tests (`main_test.go`) cover config parsing, timestamp extraction, cert pool
loading, and the full `collect` → msgpack encode path without cgo.

Integration tests spin up the plugin image against real Filebeat containers.
`test-integration` verifies Lumberjack v1/v2 compatibility across Filebeat
generations; `test-transport` verifies the three TLS modes (certs are generated
fresh per run, no fixtures committed).

## Important caveats

- **Single instance per process.** The Go input callback gets no per-instance
  context, so state is package-level. Run one `[INPUT] beats` per Fluent Bit
  process (or extend `FLBPluginInit` with an address-keyed registry).
- **ACK timing / durability.** The plugin ACKs a batch once its events are
  buffered internally, not after Fluent Bit flushes them downstream — the Go
  API exposes no flush-confirmation hook. This is at-least-once *up to the
  plugin boundary*; a Fluent Bit crash with records still buffered loses them
  despite the Beat having seen an ACK. For stronger guarantees, add a
  persistent queue in `consume()`.
- **Not compiled in this environment.** Build it yourself with the commands
  above; pin `go.mod` versions via `go mod tidy`.

## License

Apache-2.0 (matches both upstream libraries).
