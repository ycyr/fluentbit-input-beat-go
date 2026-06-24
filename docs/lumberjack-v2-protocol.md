# Lumberjack Protocol v2

> **Status:** No official specification document exists for v2. This reference
> was reverse-engineered from the canonical Go implementation:
> [`elastic/go-lumber`](https://github.com/elastic/go-lumber) (Apache-2.0).
>
> v2 is the default protocol for all modern Beats (Filebeat 7.x+). The wire
> format is the same framing as v1 but replaces key/value data frames with
> JSON data frames.

---

## Key differences from v1

| | v1 | v2 |
|---|---|---|
| Version byte | `'1'` (0x31) | `'2'` (0x32) |
| Data frame | `'D'` — key/value pairs | `'J'` — JSON object |
| Compressed | `'C'` — same as v1 | `'C'` — same as v1 |
| Window size | `'W'` — same as v1 | `'W'` — same as v1 |
| ACK | `'A'` — same as v1 | `'A'` — same as v1 |

---

## Frame header

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+---------------+---------------+-------------------------------+
|   version(1)  |   frame type  |     payload ...               |
+---------------------------------------------------------------+
```

- `version` = `0x32` (ASCII `'2'`)
- All numeric values are **big-endian** (network byte order).

---

## Frame types

#### `'J'` — JSON data frame (writer → reader)

Carries one log event encoded as a JSON object.

```
[4 bytes] sequence number (uint32)
[4 bytes] payload length  (uint32)
[N bytes] JSON payload    (UTF-8 encoded JSON object)
```

The JSON payload is a flat or nested object. Filebeat typically includes
fields such as `message`, `@timestamp`, `log.file.path`, `agent`, `ecs`, etc.

#### `'A'` — ack frame (reader → writer)

```
[4 bytes] sequence number (uint32)
```

Bulk acks supported: ack for N acknowledges all frames ≤ N.

#### `'W'` — window size (writer → reader)

```
[4 bytes] window size (uint32, in whole data frames)
```

#### `'C'` — compressed frame (writer → reader)

```
[4 bytes] payload length (uint32)
[N bytes] zlib-compressed payload
```

The compressed payload must contain complete frames. Decompress and process
as a normal frame stream. Frames inside may be `'J'` (v2) or `'D'` (v1).

---

## Connection handshake

1. Writer sends a `'W'` (window size) frame.
2. Writer sends `'J'` (or `'C'`) frames up to the window size.
3. Reader sends `'A'` (ack) frames as it processes batches.
4. Writer blocks when unacked frames reach the window size, then resumes.
5. Either side may close the connection after a keepalive timeout.

---

## go-lumber v2 constants

```go
const (
    CodeVersion       byte = '2'  // 0x32
    CodeWindowSize    byte = 'W'  // 0x57
    CodeJSONDataFrame byte = 'J'  // 0x4A
    CodeCompressed    byte = 'C'  // 0x43
    CodeACK           byte = 'A'  // 0x41
)
```

Source: `github.com/elastic/go-lumber/protocol/v2/protocol.go`

---

## go-lumber server defaults (as used by this plugin)

| Option | Default | Notes |
|--------|---------|-------|
| Timeout | 30s | Connection idle timeout |
| Keepalive | 3s | TCP keepalive interval |
| v1 | false | Enable via `enable_v1 true` |
| v2 | true | On by default |

Source: `github.com/elastic/go-lumber/server/server.go`
