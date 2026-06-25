// Package main implements a Fluent Bit *input* plugin that speaks the
// Beats / Lumberjack protocol (the wire format used by Filebeat and the other
// Elastic Beats shippers).
//
// It embeds the server side of github.com/elastic/go-lumber, accepts batches
// from Beats clients, ACKs them, and feeds each event into Fluent Bit as a
// MessagePack record.
//
// Build:
//
//	go build -trimpath -buildmode=c-shared -o in_beats.so .
//
// (yes, the .so works for input plugins too; the buildmode is the same)
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/elastic/go-lumber/server"
	"github.com/fluent/fluent-bit-go/input"
	"go.etcd.io/bbolt"
)

const walBucket = "records"

const pluginName = "beats"

// record wraps one decoded Beats event. ack is non-nil only on the last event
// of each go-lumber batch; collect() calls it after encoding the event into the
// msgpack buffer, so ACKs are sent after Fluent Bit has received the data.
type record struct {
	fields map[string]interface{}
	ack    func()
}

// beatsContext holds the state of the running plugin instance.
//
// NOTE: fluent-bit-go's input interface does NOT pass an instance context to
// the collect callback (FLBPluginInputCallback receives only data/size). That
// means state has to live at package level, so only ONE [INPUT] section of
// this plugin can run per Fluent Bit process. If you need several listeners,
// run several Fluent Bit processes (or extend this with a registry keyed by
// listen address, parsed out of FLBPluginInit).
type beatsContext struct {
	srv      server.Server
	records  chan record
	done     chan struct{}
	shutdown sync.Once
	wg       sync.WaitGroup
	wal      *bbolt.DB // nil when wal_path is not configured
}

var gCtx *beatsContext

//export FLBPluginRegister
func FLBPluginRegister(def unsafe.Pointer) int {
	return input.FLBPluginRegister(def, pluginName,
		"Beats/Lumberjack input (Filebeat & friends) for Fluent Bit")
}

//export FLBPluginInit
func FLBPluginInit(plugin unsafe.Pointer) int {
	// --- config -----------------------------------------------------------
	// `host`, `port` and `listen` are RESERVED keys on input plugins (Fluent
	// Bit overwrites them), so we expose a custom `address` key instead. The
	// `tls.*` keys are reserved too, hence the custom tls_active/cert/key
	// names below.
	addr := cfg(plugin, "address", "0.0.0.0:5044")
	bufSize := cfgInt(plugin, "buffer_size", 16384)

	// v1 defaults off: modern Filebeat (7.x / 8.x) is v2-only.
	enableV1 := cfgBool(plugin, "enable_v1", false)
	enableV2 := cfgBool(plugin, "enable_v2", true)

	opts := []server.Option{
		server.V1(enableV1),
		server.V2(enableV2),
		server.Timeout(30 * time.Second),
		server.Keepalive(3 * time.Second),
	}

	// --- optional TLS (server-TLS or mTLS) --------------------------------
	tlsMode := "none"
	tlsActive := cfgBool(plugin, "tls_active", false)

	// Guard against the silent-plaintext footgun: ca_file only takes effect
	// inside the TLS block below, so setting it without tls_active would give
	// an unencrypted listener with no client-cert verification — the opposite
	// of the operator's intent. Fail loudly instead.
	if !tlsActive && cfg(plugin, "ca_file", "") != "" {
		log.Printf("[%s] ca_file is set but tls_active is false; "+
			"refusing to start a plaintext listener (set tls_active true for mTLS)", pluginName)
		return input.FLB_ERROR
	}

	if tlsActive {
		certFile := cfg(plugin, "cert_file", "")
		keyFile := cfg(plugin, "key_file", "")
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			log.Printf("[%s] tls: failed to load keypair: %v", pluginName, err)
			return input.FLB_ERROR
		}
		tlsCfg := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}

		// mTLS: require and verify a client certificate when ca_file is set.
		if caFile := cfg(plugin, "ca_file", ""); caFile != "" {
			pool, err := loadCertPool(caFile)
			if err != nil {
				log.Printf("[%s] tls: failed to load CA: %v", pluginName, err)
				return input.FLB_ERROR
			}
			tlsCfg.ClientCAs = pool
			tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
			tlsMode = "mtls"
		} else {
			tlsMode = "tls"
		}

		opts = append(opts, server.TLS(tlsCfg))
	}

	// --- start the lumberjack server -------------------------------------
	srv, err := server.ListenAndServe(addr, opts...)
	if err != nil {
		log.Printf("[%s] failed to listen on %s: %v", pluginName, addr, err)
		return input.FLB_ERROR
	}
	log.Printf("[%s] listening on %s (v1=%v v2=%v tls=%s)",
		pluginName, addr, enableV1, enableV2, tlsMode)

	c := &beatsContext{
		srv:     srv,
		records: make(chan record, bufSize),
		done:    make(chan struct{}),
	}
	gCtx = c

	// --- optional WAL for stronger durability --------------------------------
	walPath := cfg(plugin, "wal_path", "")
	if walPath != "" {
		db, err := openWAL(walPath)
		if err != nil {
			log.Printf("[%s] wal: failed to open %s: %v", pluginName, walPath, err)
			return input.FLB_ERROR
		}
		c.wal = db
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			if err := replayWAL(c); err != nil {
				log.Printf("[%s] wal: replay error: %v", pluginName, err)
			}
		}()
	}

	c.wg.Add(1)
	go c.consume()

	return input.FLB_OK
}

// consume drains decoded batches from go-lumber and pushes their events onto
// the internal channel. The batch ACK is attached to the last event of each
// batch; collect() calls it after encoding that event into the msgpack buffer,
// so the Beat is not ACKed until Fluent Bit has received the data.
//
// Durability note: ACK now fires after Fluent Bit receives the buffer, not
// merely after we buffer internally. The remaining window is between the
// callback returning and Fluent Bit writing to an output — short, but real.
// The Go input API exposes no flush-confirmation hook; a persistent queue in
// consume() would be needed for stricter guarantees.
func (c *beatsContext) consume() {
	defer c.wg.Done()
	ch := c.srv.ReceiveChan()
	for {
		select {
		case <-c.done:
			return
		case batch, ok := <-ch:
			if !ok {
				return
			}
			if len(batch.Events) == 0 {
				batch.ACK()
				continue
			}

			// Pre-convert all events to maps (needed for both WAL and channel).
			maps := make([]map[string]interface{}, len(batch.Events))
			for i, ev := range batch.Events {
				m, ok := ev.(map[string]interface{})
				if !ok {
					m = map[string]interface{}{"message": ev}
				}
				maps[i] = m
			}

			// Write the whole batch to WAL atomically before pushing to channel.
			var walKey []byte
			if c.wal != nil {
				if data, err := json.Marshal(maps); err == nil {
					_ = c.wal.Update(func(tx *bbolt.Tx) error {
						b := tx.Bucket([]byte(walBucket))
						seq, _ := b.NextSequence()
						walKey = make([]byte, 8)
						binary.BigEndian.PutUint64(walKey, seq)
						return b.Put(walKey, data)
					})
				} else {
					log.Printf("[%s] wal: marshal error, skipping WAL for batch: %v", pluginName, err)
				}
			}

			last := len(maps) - 1
			for i, fields := range maps {
				r := record{fields: fields}
				if i == last {
					capturedKey := walKey
					r.ack = func() {
						batch.ACK()
						if c.wal != nil && capturedKey != nil {
							_ = c.wal.Update(func(tx *bbolt.Tx) error {
								return tx.Bucket([]byte(walBucket)).Delete(capturedKey)
							})
						}
					}
				}
				select {
				case c.records <- r:
				case <-c.done:
					batch.ACK()
					return
				}
			}
		}
	}
}

// FLBPluginInputCallback is polled by the Fluent Bit engine. It returns a
// MessagePack buffer containing zero or more records. Each record is the
// 2-element array [timestamp, map] that Fluent Bit expects; concatenating
// several of them forms a valid msgpack stream.
//
//export FLBPluginInputCallback
func FLBPluginInputCallback(data *unsafe.Pointer, size *C.size_t) int {
	buf := collect(gCtx, time.Second, time.Now())
	if len(buf) == 0 {
		*size = 0
		return input.FLB_OK
	}

	// The buffer must be C-allocated; Fluent Bit core takes ownership and
	// frees it. Do NOT free it yourself in the cleanup callback.
	*data = C.CBytes(buf)
	*size = C.size_t(len(buf))
	return input.FLB_OK
}

// collect waits up to `wait` for the first buffered record, then
// opportunistically drains whatever else is already queued (up to maxBatch)
// into a single MessagePack stream. It returns nil when the context is unset or
// nothing arrives in time. This is the cgo-free core of FLBPluginInputCallback,
// split out so it can be unit-tested without the C buffer plumbing.
func collect(c *beatsContext, wait time.Duration, now time.Time) []byte {
	if c == nil {
		return nil
	}

	const maxBatch = 2048

	// Block briefly for the first record so we don't spin in a tight loop,
	// then opportunistically drain whatever else is already queued.
	first, ok := waitRecord(c, wait)
	if !ok {
		return nil
	}

	enc := input.NewEncoder()
	buf := appendRecord(nil, enc, first.fields, now)
	if first.ack != nil {
		first.ack()
	}
	for i := 1; i < maxBatch; i++ {
		select {
		case r := <-c.records:
			buf = appendRecord(buf, enc, r.fields, now)
			if r.ack != nil {
				r.ack()
			}
		default:
			return buf
		}
	}
	return buf
}

// waitRecord returns the next record, or a zero record + false on timeout/shutdown.
func waitRecord(c *beatsContext, d time.Duration) (record, bool) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case r, ok := <-c.records:
		return r, ok
	case <-t.C:
		return record{}, false
	case <-c.done:
		return record{}, false
	}
}

// appendRecord msgpack-encodes one [timestamp, record] entry and appends it.
func appendRecord(buf []byte, enc *input.FLBEncoder, rec map[string]interface{}, fallback time.Time) []byte {
	entry := []interface{}{
		input.FLBTime{Time: recordTime(rec, fallback)},
		rec,
	}
	packed, err := enc.Encode(entry)
	if err != nil {
		log.Printf("[%s] encode error: %v", pluginName, err)
		return buf
	}
	return append(buf, packed...)
}

// recordTime prefers the Beat's own "@timestamp" (RFC3339) when present.
func recordTime(rec map[string]interface{}, fallback time.Time) time.Time {
	if v, ok := rec["@timestamp"]; ok {
		if s, ok := v.(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				return t
			}
		}
	}
	return fallback
}

//export FLBPluginInputCleanupCallback
func FLBPluginInputCleanupCallback(data unsafe.Pointer) int {
	// Intentionally a no-op. This callback is for cleaning up *our* resources,
	// not the buffer handed to the engine in FLBPluginInputCallback — the
	// engine owns and frees that. Freeing `data` here would be a double-free.
	return input.FLB_OK
}

//export FLBPluginExit
func FLBPluginExit() int {
	c := gCtx
	if c == nil {
		return input.FLB_OK
	}
	c.shutdown.Do(func() {
		close(c.done)
		if c.srv != nil {
			_ = c.srv.Close()
		}
	})
	c.wg.Wait()
	if c.wal != nil {
		_ = c.wal.Close()
	}
	return input.FLB_OK
}

// --- WAL helpers ----------------------------------------------------------

// openWAL opens (or creates) the bbolt database at path and ensures the
// records bucket exists.
func openWAL(path string) (*bbolt.DB, error) {
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(walBucket))
		return err
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// replayWAL reads all unprocessed entries from the WAL and pushes them onto
// c.records. Each entry's ack deletes it from the WAL; entries that were
// ACKed to the Beat but not yet deleted will be re-delivered to Fluent Bit.
func replayWAL(c *beatsContext) error {
	// Collect into memory first so the read transaction closes before we start
	// pushing. Pushing blocks when the channel is full; a blocked push must not
	// hold an open read-tx or WAL deletes (write-tx) would deadlock bbolt.
	// Memory is bounded: consume() blocks on the channel before writing more WAL
	// entries, so the WAL never exceeds buffer_size events (~8 MB at defaults).
	var replayed []record
	if err := c.wal.View(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(walBucket)).ForEach(func(k, v []byte) error {
			var events []map[string]interface{}
			if err := json.Unmarshal(v, &events); err != nil {
				log.Printf("[%s] wal: skipping corrupt entry: %v", pluginName, err)
				return nil
			}
			key := make([]byte, 8)
			copy(key, k)
			last := len(events) - 1
			for i, fields := range events {
				r := record{fields: fields}
				if i == last {
					walKey := key
					r.ack = func() {
						_ = c.wal.Update(func(tx *bbolt.Tx) error {
							return tx.Bucket([]byte(walBucket)).Delete(walKey)
						})
					}
				}
				replayed = append(replayed, r)
			}
			return nil
		})
	}); err != nil {
		return err
	}
	if len(replayed) > 0 {
		log.Printf("[%s] wal: replaying %d events", pluginName, len(replayed))
	}
	for _, r := range replayed {
		select {
		case c.records <- r:
		case <-c.done:
			return nil
		}
	}
	return nil
}

// --- small config helpers -------------------------------------------------

func cfg(plugin unsafe.Pointer, key, def string) string {
	if v := strings.TrimSpace(input.FLBPluginConfigKey(plugin, key)); v != "" {
		return v
	}
	return def
}

func cfgBool(plugin unsafe.Pointer, key string, def bool) bool {
	return parseBool(input.FLBPluginConfigKey(plugin, key), def)
}

func cfgInt(plugin unsafe.Pointer, key string, def int) int {
	return parseInt(input.FLBPluginConfigKey(plugin, key), def)
}

// parseBool interprets the usual truthy spellings; anything non-empty and
// non-truthy is false, and an empty/whitespace value falls back to def.
func parseBool(raw string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "":
		return def
	case "on", "true", "1", "yes", "y":
		return true
	default:
		return false
	}
}

// parseInt accepts a plain base-10 integer. Empty, non-numeric, or a
// non-positive value all fall back to def.
func parseInt(raw string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// loadCertPool reads a PEM-encoded CA bundle and returns a cert pool for mTLS.
func loadCertPool(caFile string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, os.ErrInvalid
	}
	return pool, nil
}

func main() {} // required: package main must build as a shared object
