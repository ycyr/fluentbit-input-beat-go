package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ugorji/go/codec"
)

func TestParseBool(t *testing.T) {
	cases := []struct {
		raw  string
		def  bool
		want bool
	}{
		{"", false, false},   // empty falls back to def
		{"", true, true},     // empty falls back to def
		{"   ", true, true},  // whitespace-only is empty
		{"true", false, true},
		{"TRUE", false, true},
		{" On ", false, true}, // trimmed + case-folded
		{"1", false, true},
		{"yes", false, true},
		{"y", false, true},
		{"false", true, false},
		{"0", true, false},
		{"no", true, false},
		{"garbage", true, false}, // non-empty non-truthy is false, not def
	}
	for _, c := range cases {
		if got := parseBool(c.raw, c.def); got != c.want {
			t.Errorf("parseBool(%q, %v) = %v, want %v", c.raw, c.def, got, c.want)
		}
	}
}

func TestParseInt(t *testing.T) {
	cases := []struct {
		raw  string
		def  int
		want int
	}{
		{"", 16384, 16384},      // empty -> def
		{"  ", 16384, 16384},    // whitespace -> def
		{"4096", 16384, 4096},   // plain value
		{" 4096 ", 16384, 4096}, // trimmed
		{"0", 16384, 16384},     // zero -> def (guards make(chan, 0))
		{"-5", 16384, 16384},    // sign is non-numeric -> def
		{"12ab", 16384, 16384},  // trailing junk -> def
		{"1_000", 16384, 16384}, // underscore -> def
	}
	for _, c := range cases {
		if got := parseInt(c.raw, c.def); got != c.want {
			t.Errorf("parseInt(%q, %d) = %d, want %d", c.raw, c.def, got, c.want)
		}
	}
}

func TestRecordTime(t *testing.T) {
	fallback := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, c := range []struct {
		name string
		ts   string
		want time.Time
	}{
		// fractional seconds (.5 = 500ms)
		{"RFC3339Nano fractional", "2026-06-24T10:11:12.5Z", time.Date(2026, 6, 24, 10, 11, 12, 500_000_000, time.UTC)},
		// no sub-seconds — common in Filebeat 6.x and syslog-style sources
		{"RFC3339 no nanos", "2026-06-24T10:11:12Z", time.Date(2026, 6, 24, 10, 11, 12, 0, time.UTC)},
		// full nanosecond precision (Filebeat 7/8 default)
		{"RFC3339Nano full nanos", "2026-06-24T10:11:12.123456789Z", time.Date(2026, 6, 24, 10, 11, 12, 123_456_789, time.UTC)},
		// timezone offset — must normalise to UTC
		{"RFC3339 with offset", "2026-06-24T12:11:12+02:00", time.Date(2026, 6, 24, 10, 11, 12, 0, time.UTC)},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			rec := map[string]interface{}{"@timestamp": c.ts}
			got := recordTime(rec, fallback)
			if !got.Equal(c.want) {
				t.Errorf("recordTime(%q) = %v, want %v", c.ts, got, c.want)
			}
		})
	}

	for name, rec := range map[string]map[string]interface{}{
		"missing key":    {"message": "hi"},
		"non-string ts":  {"@timestamp": 12345},
		"unparseable ts": {"@timestamp": "not-a-date"},
		"wrong format":   {"@timestamp": "2026/06/24 10:11:12"},
	} {
		rec := rec
		t.Run("falls back when "+name, func(t *testing.T) {
			if got := recordTime(rec, fallback); !got.Equal(fallback) {
				t.Errorf("got %v, want fallback %v", got, fallback)
			}
		})
	}
}

func TestLoadCertPool(t *testing.T) {
	dir := t.TempDir()

	caPEM := generateCACertPEM(t)
	good := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(good, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCertPool(good); err != nil {
		t.Errorf("loadCertPool(valid CA) returned error: %v", err)
	}

	bad := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(bad, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCertPool(bad); err == nil {
		t.Error("loadCertPool(non-PEM) = nil error, want failure")
	}

	if _, err := loadCertPool(filepath.Join(dir, "does-not-exist.pem")); err == nil {
		t.Error("loadCertPool(missing file) = nil error, want failure")
	}
}

// TestCollectEndToEnd exercises the cgo-free core of the collect callback: it
// stages records on the channel, runs collect, then decodes the MessagePack
// stream it produced and verifies both the record bodies and the per-record
// timestamp (sourced from @timestamp, else the supplied receive time).
func TestCollectEndToEnd(t *testing.T) {
	c := &beatsContext{
		records: make(chan record, 8),
		done:    make(chan struct{}),
	}

	atTS := time.Date(2026, 6, 24, 10, 11, 12, 0, time.UTC)
	recvTS := time.Date(2026, 6, 24, 9, 0, 0, 0, time.UTC)
	c.records <- record{fields: map[string]interface{}{"@timestamp": atTS.Format(time.RFC3339Nano), "message": "one"}}
	c.records <- record{fields: map[string]interface{}{"message": "two"}} // no @timestamp -> recvTS

	buf := collect(c, time.Second, recvTS)
	if len(buf) == 0 {
		t.Fatal("collect produced no buffer despite queued records")
	}

	entries := decodeFLBStream(t, buf)
	if len(entries) != 2 {
		t.Fatalf("decoded %d entries, want 2", len(entries))
	}

	if got := entries[0].rec["message"]; got != "one" {
		t.Errorf("entry0 message = %v, want \"one\"", got)
	}
	if !entries[0].ts.Equal(atTS) {
		t.Errorf("entry0 timestamp = %v, want %v (from @timestamp)", entries[0].ts, atTS)
	}
	if got := entries[1].rec["message"]; got != "two" {
		t.Errorf("entry1 message = %v, want \"two\"", got)
	}
	if !entries[1].ts.Equal(recvTS) {
		t.Errorf("entry1 timestamp = %v, want fallback %v", entries[1].ts, recvTS)
	}
}

// TestCollectEmpty verifies collect blocks ~wait for the first record, then
// returns nil (no spinning, no allocation) when the queue stays empty.
func TestCollectEmpty(t *testing.T) {
	c := &beatsContext{
		records: make(chan record),
		done:    make(chan struct{}),
	}

	start := time.Now()
	buf := collect(c, 200*time.Millisecond, time.Now())
	elapsed := time.Since(start)

	if buf != nil {
		t.Errorf("buf = %v, want nil on an empty queue", buf)
	}
	if elapsed < 150*time.Millisecond {
		t.Errorf("returned after %v; expected it to block ~the wait duration", elapsed)
	}
}

// TestCollectDoneUnblocks ensures a closed done channel releases waitRecord even
// if no record ever arrives (the shutdown path).
func TestCollectDoneUnblocks(t *testing.T) {
	c := &beatsContext{
		records: make(chan record),
		done:    make(chan struct{}),
	}
	close(c.done)

	done := make(chan []byte, 1)
	go func() { done <- collect(c, 5*time.Second, time.Now()) }()

	select {
	case buf := <-done:
		if buf != nil {
			t.Errorf("buf = %v, want nil after shutdown", buf)
		}
	case <-time.After(time.Second):
		t.Fatal("collect did not return promptly after done was closed")
	}
}

func TestCollectNilContext(t *testing.T) {
	if buf := collect(nil, time.Second, time.Now()); buf != nil {
		t.Errorf("collect(nil) = %v, want nil", buf)
	}
}

// TestCollectFilebeatEvents verifies that realistic Filebeat 8.x and 6.x event
// payloads pass through collect unchanged, including @metadata.
func TestCollectFilebeatEvents(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)

	// Filebeat 8.x event structure (ECS, agent.*, log.file.path)
	fb8 := map[string]interface{}{
		"@timestamp": "2026-06-24T12:34:56.789Z",
		"@metadata":  map[string]interface{}{"beat": "filebeat", "version": "8.13.4"},
		"message":    "service started",
		"log":        map[string]interface{}{"file": map[string]interface{}{"path": "/var/log/app.log"}, "offset": int64(0)},
		"agent":      map[string]interface{}{"name": "my-host", "type": "filebeat", "version": "8.13.4"},
		"ecs":        map[string]interface{}{"version": "8.0.0"},
		"host":       map[string]interface{}{"name": "my-host"},
		"input":      map[string]interface{}{"type": "log"},
	}

	// Filebeat 6.x event structure (prospectors, beat.*, source field)
	fb6 := map[string]interface{}{
		"@timestamp": "2026-06-24T12:34:57Z",
		"@metadata":  map[string]interface{}{"beat": "filebeat", "version": "6.8.23"},
		"message":    "connection established",
		"source":     "/var/log/app.log",
		"offset":     int64(100),
		"beat":       map[string]interface{}{"name": "my-host", "hostname": "my-host", "version": "6.8.23"},
		"prospector": map[string]interface{}{"type": "log"},
	}

	c := &beatsContext{
		records: make(chan record, 4),
		done:    make(chan struct{}),
	}
	c.records <- record{fields: fb8}
	c.records <- record{fields: fb6}

	entries := decodeFLBStream(t, collect(c, time.Millisecond, now))
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	e8 := entries[0]
	if want := time.Date(2026, 6, 24, 12, 34, 56, 789_000_000, time.UTC); !e8.ts.Equal(want) {
		t.Errorf("fb8 ts = %v, want %v", e8.ts, want)
	}
	if e8.rec["message"] != "service started" {
		t.Errorf("fb8 message = %v", e8.rec["message"])
	}
	if e8.rec["@metadata"] == nil {
		t.Error("fb8 @metadata missing — want it preserved")
	}

	e6 := entries[1]
	if want := time.Date(2026, 6, 24, 12, 34, 57, 0, time.UTC); !e6.ts.Equal(want) {
		t.Errorf("fb6 ts = %v, want %v", e6.ts, want)
	}
	if e6.rec["source"] != "/var/log/app.log" {
		t.Errorf("fb6 source = %v", e6.rec["source"])
	}
	if e6.rec["@metadata"] == nil {
		t.Error("fb6 @metadata missing — want it preserved")
	}
}

// TestCollectMaxBatch verifies collect drains at most 2048 records per call,
// leaving the remainder for the next call.
func TestCollectMaxBatch(t *testing.T) {
	const total = 2048 + 5
	c := &beatsContext{
		records: make(chan record, total),
		done:    make(chan struct{}),
	}
	for i := 0; i < total; i++ {
		c.records <- record{fields: map[string]interface{}{"n": i}}
	}
	now := time.Now()

	first := decodeFLBStream(t, collect(c, time.Millisecond, now))
	if len(first) != 2048 {
		t.Errorf("first collect: %d entries, want 2048", len(first))
	}
	second := decodeFLBStream(t, collect(c, time.Millisecond, now))
	if len(second) != 5 {
		t.Errorf("second collect: %d entries, want 5", len(second))
	}
}

// TestACKDelay verifies that batch ACKs are fired inside collect() after the
// records are encoded into the buffer — not before collect() is called.
func TestACKDelay(t *testing.T) {
	c := &beatsContext{
		records: make(chan record, 4),
		done:    make(chan struct{}),
	}

	var acked atomic.Int32
	ack := func() { acked.Add(1) }

	// Simulate a two-event batch: ack attached only to last event.
	c.records <- record{fields: map[string]interface{}{"n": 0}}
	c.records <- record{fields: map[string]interface{}{"n": 1}, ack: ack}

	if acked.Load() != 0 {
		t.Fatal("ACK fired before collect() was called")
	}
	collect(c, time.Millisecond, time.Now())
	if got := acked.Load(); got != 1 {
		t.Fatalf("acked = %d after collect(), want 1", got)
	}
}

// --- decode helpers -------------------------------------------------------

type flbEntry struct {
	ts  time.Time
	rec map[string]interface{}
}

// flbTimeExt mirrors input.FLBTime's wire format (8 bytes: BE uint32 seconds,
// BE uint32 nanoseconds) so the test can read back what the plugin encoded.
type flbTimeExt struct{}

func (flbTimeExt) WriteExt(interface{}) []byte { panic("encode unused in tests") }

func (flbTimeExt) ReadExt(dst interface{}, b []byte) {
	if len(b) != 8 {
		panic("unexpected FLBTime ext length")
	}
	sec := binary.BigEndian.Uint32(b[:4])
	nsec := binary.BigEndian.Uint32(b[4:])
	*dst.(*time.Time) = time.Unix(int64(sec), int64(nsec)).UTC()
}

// decodeFLBStream decodes a concatenation of [FLBTime, map] msgpack arrays,
// which is exactly what collect emits.
func decodeFLBStream(t *testing.T, buf []byte) []flbEntry {
	t.Helper()

	h := &codec.MsgpackHandle{}
	h.RawToString = true
	h.MapType = reflect.TypeOf(map[string]interface{}{})
	h.SetBytesExt(reflect.TypeOf(time.Time{}), 0, flbTimeExt{})

	dec := codec.NewDecoderBytes(buf, h)
	var out []flbEntry
	for {
		var entry []interface{}
		if err := dec.Decode(&entry); err != nil {
			break // EOF / end of concatenated stream
		}
		if len(entry) != 2 {
			t.Fatalf("entry has %d elements, want 2 ([time, map])", len(entry))
		}
		ts, ok := entry[0].(time.Time)
		if !ok {
			t.Fatalf("entry[0] is %T, want time.Time (FLBTime ext)", entry[0])
		}
		rec, ok := entry[1].(map[string]interface{})
		if !ok {
			t.Fatalf("entry[1] is %T, want map[string]interface{}", entry[1])
		}
		out = append(out, flbEntry{ts: ts, rec: rec})
	}
	return out
}

// generateCACertPEM produces a self-signed CA certificate in PEM form for the
// loadCertPool test, so no fixture files need to live in the repo.
func generateCACertPEM(t *testing.T) []byte {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
