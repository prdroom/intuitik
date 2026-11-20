//go:build kind

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/prdroom/intuitik/tools/perfjudge"
)

// control is the C6 control client (packet 6.3): ServiceAccount control, the same role as
// the panel. It writes one cause record per request it makes, with the Audit-Id response
// header and, for a watch, the terminal event it received.
type control struct {
	c       *cluster
	client  *http.Client
	mu      sync.Mutex
	records [][]byte
}

func (k *control) record(r perfjudge.Record) {
	b, _ := json.Marshal(r)
	k.mu.Lock()
	k.records = append(k.records, b)
	k.mu.Unlock()
	say("PERF-CONTROL record %s", b)
}

// list lists the Pods of namespace ns and returns the list's resourceVersion.
func (k *control) list(ns string) (string, error) {
	path := "/api/v1/namespaces/" + ns + "/pods"
	status, b, id, err := k.c.do(context.Background(), k.client, "GET", path, nil, metadataAccept)
	if err != nil || status != 200 {
		return "", fmt.Errorf("control list %s: status %d %v", path, status, err)
	}
	if id == "" {
		return "", fmt.Errorf("control list %s: no Audit-Id header", path)
	}
	k.record(perfjudge.Record{AuditID: id, Verb: "list", Path: path})
	var l struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
	}
	json.Unmarshal(b, &l)
	return l.Metadata.ResourceVersion, nil
}

// watch watches the Pods of namespace ns from resourceVersion rv. With cancelAfter > 0 the
// client cancels the watch after that time and records no event; otherwise it reads until
// the server ends the stream (at most 10 s) and records the terminal event it received.
func (k *control) watch(ns, rv string, cancelAfter time.Duration) (*perfjudge.RecordEvent, error) {
	path := "/api/v1/namespaces/" + ns + "/pods?watch=1&resourceVersion=" + rv
	limit := 10 * time.Second
	if cancelAfter > 0 {
		limit = cancelAfter
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", k.c.server+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("control watch %s: %v", path, err)
	}
	defer resp.Body.Close()
	id := resp.Header.Get("Audit-Id")
	if resp.StatusCode != 200 || id == "" {
		return nil, fmt.Errorf("control watch %s: status %d Audit-Id %q", path, resp.StatusCode, id)
	}
	var last *perfjudge.RecordEvent
	dec := json.NewDecoder(resp.Body)
	for {
		var ev struct {
			Type   string `json:"type"`
			Object struct {
				Code int `json:"code"`
			} `json:"object"`
		}
		err := dec.Decode(&ev)
		if err != nil {
			if cancelAfter > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				k.record(perfjudge.Record{AuditID: id, Verb: "watch", Path: path})
				return nil, nil
			}
			if errors.Is(err, io.EOF) && last != nil {
				k.record(perfjudge.Record{AuditID: id, Verb: "watch", Path: path, Event: last})
				return last, nil
			}
			k.record(perfjudge.Record{AuditID: id, Verb: "watch", Path: path, Event: last})
			return last, fmt.Errorf("control watch %s ended without a terminal event within %s: %v", path, limit, err)
		}
		last = &perfjudge.RecordEvent{Type: ev.Type}
		if ev.Type == "ERROR" {
			last.Code = ev.Object.Code
		}
	}
}

// noisyWindow is C6 window (2): the client lists the Pods of perf-001 every interval.
func (k *control) noisyWindow(t *testing.T, length, interval time.Duration) perfjudge.Window {
	t.Helper()
	w := perfjudge.Window{Span: perfjudge.Span{Start: time.Now()}, Interval: interval}
	for at := 500 * time.Millisecond; at < length; at += interval {
		time.Sleep(time.Until(w.Span.Start.Add(at)))
		if _, err := k.list("perf-001"); err != nil {
			t.Errorf("PERF-HARNESS window=noisy %v", err)
		}
	}
	time.Sleep(time.Until(w.Span.Start.Add(length)))
	w.Span.End = time.Now()
	say("PERF-WINDOW noisy start=%s end=%s interval=%s", stamp(w.Span.Start), stamp(w.Span.End), interval)
	return w
}

// rowA is row a of the C6 table: a watch of perf-001 from resourceVersion 1 that must end
// with an ERROR event of code 410 within 10 s (a named stop otherwise, packet 8), then a
// list of perf-001.
func (k *control) rowA(t *testing.T) string {
	t.Helper()
	ev, err := k.watch("perf-001", "1", 0)
	if err != nil || ev == nil || ev.Type != "ERROR" || ev.Code != 410 {
		t.Errorf("PERF-HARNESS STOP-4 named stop: the resourceVersion=1 watch did not end with an ERROR 410 event within 10 s: event %+v, %v", ev, err)
	}
	time.Sleep(500 * time.Millisecond)
	rv, err := k.list("perf-001")
	if err != nil {
		t.Errorf("PERF-HARNESS row a list: %v", err)
	}
	return rv
}

// window410 is C6 window (3a) (row a only) or (3b) (rows a to d, in order), 60 s.
func (k *control) window410(t *testing.T, name string, mixed bool) perfjudge.Window {
	t.Helper()
	w := perfjudge.Window{Span: perfjudge.Span{Start: time.Now()}}
	time.Sleep(time.Second)
	rv := k.rowA(t)
	if mixed {
		// Row b: a watch of perf-001 the client cancels (no 410 record), then a list of
		// perf-001 within 5 s.
		if _, err := k.watch("perf-001", rv, 500*time.Millisecond); err != nil {
			t.Errorf("PERF-HARNESS row b watch: %v", err)
		}
		time.Sleep(time.Second)
		if _, err := k.list("perf-001"); err != nil {
			t.Errorf("PERF-HARNESS row b list: %v", err)
		}
		// Row c: a list of perf-002, where no watch ended.
		if _, err := k.list("perf-002"); err != nil {
			t.Errorf("PERF-HARNESS row c list: %v", err)
		}
		// Row d: a second list of perf-001 after row a's 410 (its record is consumed).
		if _, err := k.list("perf-001"); err != nil {
			t.Errorf("PERF-HARNESS row d list: %v", err)
		}
	}
	time.Sleep(time.Until(w.Span.Start.Add(60 * time.Second)))
	w.Span.End = time.Now()
	say("PERF-WINDOW %s start=%s end=%s", name, stamp(w.Span.Start), stamp(w.Span.End))
	return w
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }
