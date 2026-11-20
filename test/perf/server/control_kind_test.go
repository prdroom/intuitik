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

// control is the C6 control client of one cluster (packet 6.3): ServiceAccount control,
// the same role as the panel. It writes one cause record per request it makes, with its
// cluster, the Audit-Id response header and, for a watch, the terminal event it received.
// Each cluster has its own control client and records (packet r5.1 6.2).
type control struct {
	c           *cluster
	client      *http.Client
	mu          sync.Mutex
	records     [][]byte
	rowAWatches []rowAWatch
}

func (k *control) record(r perfjudge.Record) {
	b, _ := json.Marshal(r)
	k.mu.Lock()
	k.records = append(k.records, b)
	k.mu.Unlock()
	say("PERF-CONTROL record %s", b)
}

// pods and cronjobs are the collections of the control windows: Pods for the noisy window
// in the measurement cluster, batch/v1 CronJobs for windows 3a and 3b in the reproduction
// cluster (packet r5 6.3).
func pods(ns string) string     { return "/api/v1/namespaces/" + ns + "/pods" }
func cronjobs(ns string) string { return "/apis/batch/v1/namespaces/" + ns + "/cronjobs" }

// list lists the collection path and returns the list's resourceVersion.
func (k *control) list(path string) (string, error) {
	status, b, id, err := k.c.do(context.Background(), k.client, "GET", path, nil, metadataAccept)
	if err != nil || status != 200 {
		return "", fmt.Errorf("control list %s: status %d %v", path, status, err)
	}
	if id == "" {
		return "", fmt.Errorf("control list %s: no Audit-Id header", path)
	}
	k.record(perfjudge.Record{Cluster: k.c.name, AuditID: id, Verb: "list", Path: path})
	var l struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
	}
	json.Unmarshal(b, &l)
	return l.Metadata.ResourceVersion, nil
}

// watch watches the collection from resourceVersion rv. With cancelAfter > 0 the client
// cancels the watch after that time and records no event; otherwise it reads until the
// server ends the stream (at most 10 s) and records the terminal event it received.
func (k *control) watch(collection, rv string, cancelAfter time.Duration) (*perfjudge.RecordEvent, string, error) {
	path := collection + "?watch=1&resourceVersion=" + rv
	limit := 10 * time.Second
	if cancelAfter > 0 {
		limit = cancelAfter
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", k.c.server+path, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("control watch %s: %v", path, err)
	}
	defer resp.Body.Close()
	id := resp.Header.Get("Audit-Id")
	if resp.StatusCode != 200 || id == "" {
		return nil, id, fmt.Errorf("control watch %s: status %d Audit-Id %q", path, resp.StatusCode, id)
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
				k.record(perfjudge.Record{Cluster: k.c.name, AuditID: id, Verb: "watch", Path: path})
				return nil, id, nil
			}
			if errors.Is(err, io.EOF) && last != nil {
				k.record(perfjudge.Record{Cluster: k.c.name, AuditID: id, Verb: "watch", Path: path, Event: last})
				return last, id, nil
			}
			k.record(perfjudge.Record{Cluster: k.c.name, AuditID: id, Verb: "watch", Path: path, Event: last})
			return last, id, fmt.Errorf("control watch %s ended without a terminal event within %s: %v", path, limit, err)
		}
		last = &perfjudge.RecordEvent{Type: ev.Type}
		if ev.Type == "ERROR" {
			last.Code = ev.Object.Code
		}
	}
}

// noisyWindow is C6 window (2) in the measurement cluster: the client lists the Pods of
// perf-001 every interval.
func (k *control) noisyWindow(t *testing.T, length, interval time.Duration) perfjudge.Window {
	t.Helper()
	w := perfjudge.Window{Span: perfjudge.Span{Start: time.Now()}, Interval: interval}
	for at := 500 * time.Millisecond; at < length; at += interval {
		time.Sleep(time.Until(w.Span.Start.Add(at)))
		if _, err := k.list(pods("perf-001")); err != nil {
			errorf(t, "PERF-HARNESS window=noisy %v", err)
		}
	}
	time.Sleep(time.Until(w.Span.Start.Add(length)))
	w.Span.End = time.Now()
	say("PERF-WINDOW noisy start=%s end=%s interval=%s", stamp(w.Span.Start), stamp(w.Span.End), interval)
	return w
}

// compacted is the setup probe of COMPACT-410-T12 (packet r5 6.3), run in the reproduction
// cluster before window (3a), outside every window: an exact list of the CronJobs of
// perf-001 at resourceVersion 1, repeated until the API server answers 410 (that version
// is compacted away). 200 means it is still readable:
// wait and ask again. One 120 s deadline covers every request and wait, and each request
// gets the remaining time. Any other answer, a read failure or the deadline is the packet
// 8 named stop. The result is only "RV=1 LIST no longer readable"; it is not evidence
// that a watch ends with 410.
func (k *control) compacted(t *testing.T) {
	t.Helper()
	t0 := time.Now()
	deadline := t0.Add(120 * time.Second)
	path := cronjobs("perf-001") + "?resourceVersion=1&resourceVersionMatch=Exact&limit=1"
	for {
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), left)
		status, _, id, err := k.c.do(ctx, k.client, "GET", path, nil, metadataAccept)
		cancel()
		say("PERF-SETUP probe (outside the product window and windows 3a/3b) auditID=%s uri=%s status=%d at=%s elapsed_ms=%d err=%v",
			id, path, status, stamp(time.Now()), time.Since(t0).Milliseconds(), err)
		switch {
		case err != nil:
			fatalf(t, "PERF verdict=ERROR STOP-4 named stop (packet 8): RV=1 probe read failure: %v", err)
		case status == http.StatusGone:
			say("PERF-SETUP RV=1 LIST no longer readable after %d ms", time.Since(t0).Milliseconds())
			return
		case status != http.StatusOK:
			fatalf(t, "PERF verdict=ERROR STOP-4 named stop (packet 8): RV=1 probe answered %d", status)
		}
		wait := 2 * time.Second
		if left := time.Until(deadline); left < wait {
			wait = left
		}
		time.Sleep(wait)
	}
	fatalf(t, "PERF verdict=ERROR STOP-4 named stop (packet 8): RV=1 LIST still readable after 120 s")
}

// rowA is row a of the C6 table: a watch of the CronJobs of perf-001 from resourceVersion
// 1 that must end with an ERROR event of code 410 within 10 s (a named stop otherwise,
// packet 8), then a list of the CronJobs of perf-001. It keeps the watch's Audit-Id for
// rowAEvidence.
func (k *control) rowA(t *testing.T, window string) string {
	t.Helper()
	ev, id, err := k.watch(cronjobs("perf-001"), "1", 0)
	if err != nil || ev == nil || ev.Type != "ERROR" || ev.Code != 410 {
		errorf(t, "PERF-HARNESS STOP-4 named stop: the resourceVersion=1 cronjobs watch did not end with an ERROR 410 event within 10 s: window=%s auditID=%s event %+v, %v", window, id, ev, err)
	}
	k.rowAWatches = append(k.rowAWatches, rowAWatch{window: window, auditID: id, event: ev})
	time.Sleep(500 * time.Millisecond)
	rv, err := k.list(cronjobs("perf-001"))
	if err != nil {
		errorf(t, "PERF-HARNESS row a list: %v", err)
	}
	return rv
}

// rowAWatch is the watch of row a in one window, as the control client received it.
type rowAWatch struct {
	window, auditID string
	event           *perfjudge.RecordEvent
}

// rowAEvidence prints, for the watch of row a in each window, the terminal event the
// client received with its Audit-Id and the cluster's audit lines of that auditID (stage,
// verb, objectRef, code), read with its own JSON decoding, not with the judge. A watch
// whose ResponseStarted or ResponseComplete line is missing, or whose objectRef is not
// batch cronjobs of perf-001, is the packet 8 named stop "the audit log lacks the stage
// lines 6.3 needs".
func (k *control) rowAEvidence(t *testing.T, audit [][]byte) {
	t.Helper()
	for _, w := range k.rowAWatches {
		stages := map[string]bool{}
		for _, raw := range audit {
			var l struct {
				AuditID   string `json:"auditID"`
				Stage     string `json:"stage"`
				Verb      string `json:"verb"`
				ObjectRef *struct {
					APIGroup  string `json:"apiGroup"`
					Resource  string `json:"resource"`
					Namespace string `json:"namespace"`
				} `json:"objectRef"`
				ResponseStatus *struct {
					Code int `json:"code"`
				} `json:"responseStatus"`
			}
			if json.Unmarshal(raw, &l) != nil || w.auditID == "" || l.AuditID != w.auditID {
				continue
			}
			code := 0
			if l.ResponseStatus != nil {
				code = l.ResponseStatus.Code
			}
			if l.ObjectRef == nil || l.Verb != "watch" || l.ObjectRef.APIGroup != "batch" || l.ObjectRef.Resource != "cronjobs" || l.ObjectRef.Namespace != "perf-001" {
				errorf(t, "PERF-HARNESS STOP-4 named stop: row a watch %s audit line is not a batch cronjobs watch of perf-001: verb=%s objectRef=%+v", w.auditID, l.Verb, l.ObjectRef)
				continue
			}
			stages[l.Stage] = true
			say("PERF-ROWA window=%s auditID=%s audit stage=%s verb=%s group=%s resource=%s namespace=%s code=%d", w.window, w.auditID, l.Stage, l.Verb, l.ObjectRef.APIGroup, l.ObjectRef.Resource, l.ObjectRef.Namespace, code)
		}
		say("PERF-ROWA window=%s auditID=%s client event=%+v audit ResponseStarted=%v ResponseComplete=%v", w.window, w.auditID, w.event, stages["ResponseStarted"], stages["ResponseComplete"])
		if !stages["ResponseStarted"] || !stages["ResponseComplete"] {
			errorf(t, "PERF-HARNESS STOP-4 named stop: the audit log lacks a stage line of row a watch %s (window %s)", w.auditID, w.window)
		}
	}
}

// window410 is C6 window (3a) (row a only) or (3b) (rows a to d, in order), 60 s, on the
// CronJobs of the reproduction cluster.
func (k *control) window410(t *testing.T, name string, mixed bool) perfjudge.Window {
	t.Helper()
	w := perfjudge.Window{Span: perfjudge.Span{Start: time.Now()}}
	time.Sleep(time.Second)
	rv := k.rowA(t, name)
	if mixed {
		// Row b: a watch of perf-001 the client cancels (no 410 record), then a list of
		// perf-001 within 5 s.
		if _, _, err := k.watch(cronjobs("perf-001"), rv, 500*time.Millisecond); err != nil {
			errorf(t, "PERF-HARNESS row b watch: %v", err)
		}
		time.Sleep(time.Second)
		if _, err := k.list(cronjobs("perf-001")); err != nil {
			errorf(t, "PERF-HARNESS row b list: %v", err)
		}
		// Row c: a list of perf-002, where no watch ended.
		if _, err := k.list(cronjobs("perf-002")); err != nil {
			errorf(t, "PERF-HARNESS row c list: %v", err)
		}
		// Row d: a second list of perf-001 after row a's 410 (its record is consumed).
		if _, err := k.list(cronjobs("perf-001")); err != nil {
			errorf(t, "PERF-HARNESS row d list: %v", err)
		}
	}
	time.Sleep(time.Until(w.Span.Start.Add(60 * time.Second)))
	w.Span.End = time.Now()
	say("PERF-WINDOW %s start=%s end=%s", name, stamp(w.Span.Start), stamp(w.Span.End))
	return w
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }
