# Filebeat SSL configuration reference

When connecting to this plugin (or any Logstash-protocol output), Filebeat
uses `output.logstash.ssl.*` config keys. These keys map to the types in
[`elastic/elastic-agent-libs/transport/tlscommon`](https://github.com/elastic/elastic-agent-libs/tree/main/transport/tlscommon).

---

## Minimal examples

### No TLS (default)

```yaml
output.logstash:
  hosts: ["fluent-bit:5044"]
```

### Server-TLS (Filebeat verifies the server cert)

```yaml
output.logstash:
  hosts: ["fluent-bit:5044"]
  ssl:
    enabled: true
    certificate_authorities: ["/certs/ca.crt"]
    verification_mode: full   # default
```

### mTLS (server verifies the client cert too)

```yaml
output.logstash:
  hosts: ["fluent-bit:5044"]
  ssl:
    enabled: true
    certificate_authorities: ["/certs/ca.crt"]
    certificate: "/certs/client.crt"
    key:         "/certs/client.key"
```

---

## Full field reference

Source: `tlscommon.Config` in `elastic-agent-libs`.

| YAML key | Type | Default | Notes |
|---|---|---|---|
| `ssl.enabled` | bool | `true` if any `ssl.*` key is set | Set to `false` to disable TLS explicitly |
| `ssl.verification_mode` | string | `full` | `full`, `strict`, `certificate`, `none` |
| `ssl.supported_protocols` | []string | `[TLSv1.2, TLSv1.3]` | e.g. `["TLSv1.2", "TLSv1.3"]` |
| `ssl.cipher_suites` | []string | OS default | Named cipher suites |
| `ssl.curve_types` | []string | — | ECDH curves, e.g. `["P-256"]` |
| `ssl.renegotiation` | string | `never` | `never`, `once`, `freely` |
| `ssl.certificate_authorities` | []string | — | PEM CA file paths (or inline PEM) |
| `ssl.certificate` | string | — | PEM client cert path (mTLS) |
| `ssl.key` | string | — | PEM client private key path (mTLS) |
| `ssl.ca_sha256` | []string | — | CA certificate SHA-256 fingerprint pins |
| `ssl.ca_trusted_fingerprint` | string | — | Single CA fingerprint (Elastic Cloud) |
| `ssl.certificate_reload.enabled` | bool | `true` | Hot-reload certs without restart |
| `ssl.certificate_reload.reload_interval` | duration | `5s` | How often to check for cert changes |

### `verification_mode` values

| Value | Behaviour |
|-------|-----------|
| `full` | Verify hostname and cert chain (default, recommended) |
| `strict` | `full` + RFC 6125 name checks (stricter hostname matching) |
| `certificate` | Verify cert chain only; skip hostname check |
| `none` | No verification (⚠ insecure, dev/test only) |

---

## What the server (this plugin) requires

| Mode | Server needs | Client must present |
|------|-------------|---------------------|
| plaintext | nothing | nothing |
| server-TLS | `cert_file` + `key_file` | `ssl.certificate_authorities` matching the server CA |
| mTLS | + `ca_file` | + `ssl.certificate` + `ssl.key` signed by the same CA |

The server cert's SAN must match the hostname Filebeat dials. In Docker Compose
the service name is used (e.g. `DNS:fluent-bit`); in production it should be the
FQDN or IP the Filebeat host resolves to.

---

## Source

- Types: [`elastic/elastic-agent-libs/transport/tlscommon`](https://github.com/elastic/elastic-agent-libs/tree/main/transport/tlscommon)
- Filebeat docs: [Configure the Logstash output](https://www.elastic.co/guide/en/beats/filebeat/current/logstash-output.html)
