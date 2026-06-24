# Filebeat `output.logstash` configuration reference

This is the configuration Filebeat users put in `filebeat.yml` to send events
to this plugin (or any Lumberjack/Logstash-protocol receiver).

Sources:
- [`libbeat/outputs/logstash/docs/logstash.asciidoc`](https://github.com/elastic/beats/blob/main/libbeat/outputs/logstash/docs/logstash.asciidoc) (official docs)
- [`libbeat/outputs/logstash/config.go`](https://github.com/elastic/beats/blob/main/libbeat/outputs/logstash/config.go) (canonical defaults)
- [`libbeat/beat/event.go`](https://github.com/elastic/beats/blob/main/libbeat/beat/event.go) (event structure)

---

## Minimal config

```yaml
output.logstash:
  hosts: ["fluent-bit:5044"]
```

With TLS (see [filebeat-ssl-config.md](filebeat-ssl-config.md) for full SSL reference):

```yaml
output.logstash:
  hosts: ["fluent-bit:5044"]
  ssl:
    enabled: true
    certificate_authorities: ["/certs/ca.crt"]
    certificate: "/certs/client.crt"   # mTLS only
    key:         "/certs/client.key"   # mTLS only
```

---

## Configuration options

| Key | Default | Description |
|-----|---------|-------------|
| `enabled` | `true` | Enable/disable this output |
| `hosts` | — | List of `host:port` targets. Default port: 5044 |
| `compression_level` | `3` | gzip compression 0 (off) – 9 (max). 0 disables |
| `escape_html` | `false` | HTML-escape strings in JSON events |
| `workers` | `1` | Number of worker goroutines per host |
| `loadbalance` | `false` | Connect to all hosts and round-robin. `false` = random single host |
| `ttl` | `0` (disabled) | Re-establish connection after this duration (useful behind LBs) |
| `pipelining` | `2` | Async batches in-flight before blocking for ACK |
| `proxy_url` | — | SOCKS5 proxy URL, e.g. `socks5://user:pass@host:2233` |
| `proxy_use_local_resolver` | `false` | Resolve hostnames locally when using a proxy |
| `index` | beat name | Root index name (sent as `@metadata.beat`) |
| `ssl` | — | TLS options — see [filebeat-ssl-config.md](filebeat-ssl-config.md) |
| `timeout` | `30s` | Read/write timeout per connection |
| `max_retries` | `3` | Publish retries before dropping events (`-1` = infinite) |
| `bulk_max_size` | `2048` | Max events per Lumberjack batch |
| `slow_start` | `false` | Start with small batches, grow on success |
| `backoff.init` | `1s` | Initial reconnect backoff |
| `backoff.max` | `60s` | Maximum reconnect backoff |
| `queue` | — | Internal queue config (memory/disk) |

### `pipelining` note

`pipelining: 2` means Filebeat sends up to 2 batches without waiting for ACK,
then blocks. Each in-flight batch consumes up to `bulk_max_size` events.
Our plugin ACKs each batch after buffering, so increasing `pipelining` raises
throughput at the cost of more buffered-but-unconfirmed events on crash.

### Window sizing (protocol detail)

The Lumberjack window size is managed client-side. Filebeat starts at 1 and
grows by ×1.5 each successful batch until it reaches `bulk_max_size`. On
error, the window halves (floor: 1). This is separate from `pipelining` — the
window is per-connection, pipelining is per-worker.

---

## Event JSON structure (what arrives in each `'J'` frame)

Filebeat serializes a `beat.Event` to JSON for each Lumberjack v2 data frame:

```go
type Event struct {
    Timestamp time.Time  // → "@timestamp" (RFC3339Nano)
    Meta      mapstr.M   // → "@metadata" object
    Fields    mapstr.M   // → all other top-level fields
}
```

A typical Filebeat 8.x event JSON payload:

```json
{
  "@timestamp": "2024-06-24T12:34:56.789Z",
  "@metadata": {
    "beat": "filebeat",
    "version": "8.13.4"
  },
  "message": "the log line",
  "log": {
    "file": { "path": "/var/log/app/app.log" },
    "offset": 12345
  },
  "input": { "type": "log" },
  "agent": {
    "name": "my-host",
    "type": "filebeat",
    "version": "8.13.4"
  },
  "ecs": { "version": "8.0.0" },
  "host": { "name": "my-host" },
  "tags": ["beats_input_raw_event"]
}
```

### What the plugin does with the timestamp

The plugin reads `@timestamp` (RFC3339Nano) from the decoded JSON and uses it
as the Fluent Bit record timestamp (`FLBTime`). If absent or unparseable, the
receive time is used. All other fields pass through unchanged as the record
map.

### Filebeat 6.x differences

Filebeat 6.x uses `prospectors` instead of `inputs` syntax, and the event
schema differs:

```json
{
  "@timestamp": "...",
  "@metadata": { "beat": "filebeat", "version": "6.8.23" },
  "message": "the log line",
  "source": "/var/log/app/app.log",
  "offset": 12345,
  "beat": { "name": "my-host", "hostname": "my-host", "version": "6.8.23" },
  "prospector": { "type": "log" }
}
```

---

## Compatibility

| Filebeat version | Protocol | Notes |
|---|---|---|
| 5.x | v2 | `prospectors:` syntax; `beat.*` fields; no ECS |
| 6.x | v2 | `prospectors:` syntax; `beat.*` fields |
| 7.x | v2 | `inputs:` syntax; ECS fields introduced |
| 8.x | v2 | Full ECS; `agent.*` replaces `beat.*` |

Enable `enable_v1 true` in the plugin only if you need Beats older than 5.x.
All Filebeat 5.x+ uses v2 by default.
