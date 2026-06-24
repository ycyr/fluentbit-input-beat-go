# Protocol notes for the fluentbit-input-beat-go plugin

Practical notes on how this plugin uses the Lumberjack protocol — things not
covered by the upstream spec documents.

---

## Which protocol version to use

| Filebeat version | Protocol | Plugin config |
|-----------------|----------|---------------|
| < 5.x (very old) | v1 | `enable_v1 true` |
| 5.x – 8.x | v2 | `enable_v2 true` (default) |

`enable_v1` is off by default. Enable it only when connecting Beats older than 5.x.

---

## ACK timing and durability

The plugin ACKs a batch **after** buffering it in the internal `records`
channel — not after Fluent Bit flushes the records downstream.

```
Filebeat  ──batch──>  go-lumber  ──events──>  records chan  ──msgpack──>  Fluent Bit
                          ↑
                        ACK sent here (batch buffered in channel)
```

Consequence: if Fluent Bit crashes with records in the channel, those records
are **lost** even though Filebeat saw a successful ACK. This is "at-least-once
up to the plugin boundary." For stricter guarantees, add a persistent queue
inside `consume()` before calling `batch.ACK()`.

---

## Record timestamp

The plugin extracts `@timestamp` from the event (RFC3339Nano) and uses it as
the Fluent Bit record timestamp (`FLBTime`). If absent or unparseable, the
receive time is used.

`FLBTime` is a MessagePack extension type (ext 0), encoded as 8 bytes:
- bytes 0–3: Unix seconds (uint32, big-endian)
- bytes 4–7: nanoseconds (uint32, big-endian)

---

## Reserved Fluent Bit config keys

Fluent Bit **overwrites** these keys on Go input plugins — do not use them:

| Reserved | Plugin replacement |
|----------|--------------------|
| `host`, `port`, `listen` | `address` (`host:port`) |
| `tls.*` | `tls_active`, `cert_file`, `key_file`, `ca_file` |

---

## TLS modes

| `tls_active` | `ca_file` | Mode | What Filebeat must provide |
|---|---|---|---|
| false | — | Plaintext | Nothing |
| true | — | Server-TLS | Trust the server cert |
| true | set | mTLS | Trust the server cert + present a client cert signed by the CA |

Setting `ca_file` without `tls_active` is rejected at startup (would otherwise
silently start a plaintext listener with no client verification).

---

## Single instance constraint

The `FLBPluginInputCallback` signature carries no per-instance context, so all
state is package-level (`var gCtx`). Only **one** `[INPUT] beats` section per
Fluent Bit process is supported. For multiple listeners, run separate Fluent
Bit processes.

---

## References

- [Lumberjack v1 protocol spec](lumberjack-v1-protocol.md) (original, deprecated)
- [Lumberjack v2 protocol reference](lumberjack-v2-protocol.md) (derived from go-lumber source)
- [elastic/go-lumber](https://github.com/elastic/go-lumber) — server library used by this plugin
- [elastic/logstash-forwarder PROTOCOL.md](https://github.com/elastic/logstash-forwarder/blob/master/PROTOCOL.md) — upstream v1 source
- [fluent/fluent-bit-go](https://github.com/fluent/fluent-bit-go) — CGo bindings
