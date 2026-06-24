# Lumberjack Protocol v1

> **Source:** [`elastic/logstash-forwarder` PROTOCOL.md](https://github.com/elastic/logstash-forwarder/blob/master/PROTOCOL.md)
> (the `logstash-forwarder` repo is archived; this document is the only official v1 spec)
>
> **Status:** Deprecated by Elastic. The document has fallen out of date with the
> actual Beats/Logstash implementation. v2 (JSON frames) is the current protocol.
> v1 is still supported by this plugin via `enable_v1 true` for Beats < 7.x.

---

## Goals

- Encryption and authentication (via TLS)
- Compression to reduce bandwidth
- Round-trip latency must not damage throughput
- Application-level message acknowledgement

---

## Behaviour

Sequence and ack behaviour (including sliding window) is similar to TCP, but
instead of bytes, **messages** are the base unit.

A writer with a window size of 50 events can send up to 50 unacked events
before blocking. A reader can acknowledge the 'last event' received to support
bulk acknowledgements.

Reliable, ordered byte transport is provided by TCP (or TLS on top).

---

## Wire Format

### Layering

This entire protocol is layered on top of TCP or TLS.

### Frame header

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+---------------+---------------+-------------------------------+
|   version(1)  |   frame type  |     payload ...               |
+---------------------------------------------------------------+
|   payload continued...                                        |
+---------------------------------------------------------------+
```

- `version` = `0x31` (ASCII `'1'`)
- All numeric values are **big-endian** (network byte order).

---

### Frame types

#### `'D'` — data frame (writer → reader)

Carries one log event as key/value string pairs.

```
[4 bytes] sequence number (uint32)
[4 bytes] pair count      (uint32)
  repeat 'pair count' times:
    [4 bytes] key length   (uint32)
    [N bytes] key
    [4 bytes] value length (uint32)
    [N bytes] value
```

Sequence numbers roll over: if a received sequence number is less than the
previous value, a roll-over has occurred.

#### `'A'` — ack frame (reader → writer)

```
[4 bytes] sequence number (uint32)
```

Bulk acks are supported. Sending ack for sequence N acknowledges all data
frames up to and including N.

#### `'W'` — window size (writer → reader)

```
[4 bytes] window size (uint32, in whole data frames)
```

Tells the reader the maximum number of unacknowledged data frames the writer
will send before blocking.

#### `'C'` — compressed frame (writer → reader)

```
[4 bytes] payload length (uint32)
[N bytes] zlib-compressed payload
```

The compressed payload **must** contain complete frames only. When
uncompressed, the payload is a valid frame stream (i.e. process it as if
reading directly from the network). Useful for compressing many small data
frames efficiently.

---

## Implementation notes (go-lumber v1 constants)

```go
const (
    CodeVersion    byte = '1'  // 0x31
    CodeWindowSize byte = 'W'  // 0x57
    CodeDataFrame  byte = 'D'  // 0x44
    CodeCompressed byte = 'C'  // 0x43
    CodeACK        byte = 'A'  // 0x41
)
```

Source: `github.com/elastic/go-lumber/protocol/v1/protocol.go`
