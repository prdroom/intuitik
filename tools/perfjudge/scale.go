package perfjudge

import (
	"fmt"
	"sort"
	"strings"
)

// Count is the number of labelled objects of one kind in one namespace ("" for a
// cluster-scoped kind and for the Namespace objects themselves).
type Count struct {
	Namespace string
	Kind      string
	N         int
}

// Kinds are the labelled kinds of the composition, in the order of packet 6.2. Event is
// built and counted but is apart from the total (the engine never lists Events, r3).
var Kinds = []string{"Namespace", "Node", "Deployment", "ReplicaSet", "Pod", "Service", "PersistentVolumeClaim", "ConfigMap", "Secret", "Event"}

// table is the judge's own composition table of packet 6.2 (S and L) and of the XL line
// of 6.2 (L with Pods x4, Deployments, ReplicaSets and Events x4).
var table = map[string]map[string]int{
	"S": {"Namespace": 10, "Node": 3, "Deployment": 30, "ReplicaSet": 30, "Pod": 300, "Service": 90,
		"PersistentVolumeClaim": 90, "ConfigMap": 473, "Secret": 474, "Event": 100},
	"L": {"Namespace": 50, "Node": 100, "Deployment": 392, "ReplicaSet": 392, "Pod": 5000, "Service": 1500,
		"PersistentVolumeClaim": 1500, "ConfigMap": 8033, "Secret": 8033, "Event": 1000},
	"XL": {"Namespace": 50, "Node": 100, "Deployment": 1568, "ReplicaSet": 1568, "Pod": 20000, "Service": 1500,
		"PersistentVolumeClaim": 1500, "ConfigMap": 8033, "Secret": 8033, "Event": 4000},
}

// Table returns the composition of a scale and whether the scale is known.
func Table(scale string) (map[string]int, bool) {
	t, ok := table[scale]
	return t, ok
}

// ScaleResult is the C1 result of one built cluster.
type ScaleResult struct {
	Lines  []string
	OK     bool     // every kind meets the table and the read-back equals the fixture
	Errors []string // read-back counts that differ from the fixture (judge exit 2)
}

// JudgeScale checks one built cluster (C1, 6.2): the fixture's per-kind totals against the
// judge's own table, and the read-back counts per kind and namespace against the fixture
// exactly. A kind whose read-back total is below the table makes the scale short; every
// verdict of a short scale is not-measured.
func JudgeScale(scale string, fixture, readback []Count) ScaleResult {
	var r ScaleResult
	want, known := table[scale]
	if !known {
		r.Errors = append(r.Errors, "unknown scale "+scale)
		r.Lines = append(r.Lines, fmt.Sprintf("PERF scale=%s unknown", scale))
		return r
	}
	fix, fixTotal := index(fixture)
	read, readTotal := index(readback)
	var short []string
	total := map[string]int{}
	for _, k := range Kinds {
		r.Lines = append(r.Lines, fmt.Sprintf("PERF count scale=%s kind=%s want=%d fixture=%d read=%d", scale, k, want[k], fixTotal[k], readTotal[k]))
		if fixTotal[k] != want[k] {
			r.Errors = append(r.Errors, fmt.Sprintf("fixture %s=%d, table %d", k, fixTotal[k], want[k]))
		}
		if readTotal[k] < want[k] {
			short = append(short, fmt.Sprintf("%s=%d/%d", k, readTotal[k], want[k]))
		}
		if k != "Event" {
			total["want"] += want[k]
			total["read"] += readTotal[k]
		}
	}
	keys := map[string]bool{}
	for key := range fix {
		keys[key] = true
	}
	for key := range read {
		keys[key] = true
	}
	var sorted []string
	for key := range keys {
		sorted = append(sorted, key)
	}
	sort.Strings(sorted)
	for _, key := range sorted {
		if fix[key] != read[key] {
			ns, kind, _ := strings.Cut(key, "\x00")
			r.Errors = append(r.Errors, fmt.Sprintf("namespace=%q kind=%s fixture=%d read=%d", ns, kind, fix[key], read[key]))
		}
	}
	for _, e := range r.Errors {
		r.Lines = append(r.Lines, "PERF ERROR count "+e)
	}
	r.Lines = append(r.Lines, fmt.Sprintf("PERF count scale=%s total want=%d read=%d (Event apart)", scale, total["want"], total["read"]))
	r.OK = len(short) == 0 && len(r.Errors) == 0
	switch {
	case len(short) > 0:
		r.Lines = append(r.Lines, fmt.Sprintf("PERF scale=%s short %s", scale, strings.Join(short, " ")))
	case len(r.Errors) > 0:
		r.Lines = append(r.Lines, fmt.Sprintf("PERF scale=%s not-equal %d count(s)", scale, len(r.Errors)))
	default:
		r.Lines = append(r.Lines, fmt.Sprintf("PERF scale=%s ok", scale))
	}
	return r
}

// index sums counts per namespace and kind, and per kind.
func index(cs []Count) (map[string]int, map[string]int) {
	per, total := map[string]int{}, map[string]int{}
	for _, c := range cs {
		per[c.Namespace+"\x00"+c.Kind] += c.N
		total[c.Kind] += c.N
	}
	return per, total
}
