package perfjudge

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Request is one API request of the audit log: one auditID (packet 6.3 counting unit). A
// list or get counts once, at its ResponseComplete line; a watch is the pair of its
// ResponseStarted and ResponseComplete lines, and its end is the ResponseComplete
// stageTimestamp. Cluster is the kind cluster whose audit log held the request (packet 6.2
// r5.1: every binding key includes it).
type Request struct {
	Cluster   string
	AuditID   string
	Verb      string
	User      string
	Group     string
	Resource  string
	Namespace string
	URI       string
	Code      int
	Received  time.Time // requestReceivedTimestamp
	End       time.Time // stageTimestamp of the ResponseComplete line
}

// Audit is the parsed audit log.
type Audit struct {
	Lines      int
	Requests   []Request // complete requests, by received time
	Duplicates []string  // a second line of the same auditID and stage, printed, not counted
	Problems   []string  // ERROR: a line that does not decode, a list or get without its ResponseComplete, a watch without its start or end line
}

type auditLine struct {
	AuditID string `json:"auditID"`
	Stage   string `json:"stage"`
	Verb    string `json:"verb"`
	URI     string `json:"requestURI"`
	User    struct {
		Username string `json:"username"`
	} `json:"user"`
	ObjectRef *struct {
		APIGroup  string `json:"apiGroup"`
		Resource  string `json:"resource"`
		Namespace string `json:"namespace"`
	} `json:"objectRef"`
	ResponseStatus *struct {
		Code int `json:"code"`
	} `json:"responseStatus"`
	Received time.Time `json:"requestReceivedTimestamp"`
	StageAt  time.Time `json:"stageTimestamp"`
}

// ParseAudit reads the audit log lines of one cluster (one JSON event per line) into
// requests of that cluster.
func ParseAudit(cluster string, lines [][]byte) Audit {
	var a Audit
	type entry struct {
		r                 Request
		started, complete bool
		order             int
	}
	byID := map[string]*entry{}
	seen := map[string]bool{}
	for i, raw := range lines {
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		a.Lines++
		var l auditLine
		if err := json.Unmarshal(raw, &l); err != nil || l.AuditID == "" || l.Stage == "" {
			a.Problems = append(a.Problems, fmt.Sprintf("audit line %d does not decode as an event with auditID and stage", i+1))
			continue
		}
		key := l.AuditID + " " + l.Stage
		if seen[key] {
			a.Duplicates = append(a.Duplicates, key)
			continue
		}
		seen[key] = true
		e := byID[l.AuditID]
		if e == nil {
			e = &entry{order: len(byID)}
			e.r = Request{Cluster: cluster, AuditID: l.AuditID, Verb: l.Verb, User: l.User.Username, URI: l.URI, Received: l.Received}
			if l.ObjectRef != nil {
				e.r.Group, e.r.Resource, e.r.Namespace = l.ObjectRef.APIGroup, l.ObjectRef.Resource, l.ObjectRef.Namespace
			}
			byID[l.AuditID] = e
		}
		switch l.Stage {
		case "ResponseStarted":
			e.started = true
		case "ResponseComplete":
			e.complete = true
			e.r.End = l.StageAt
			if l.ResponseStatus != nil {
				e.r.Code = l.ResponseStatus.Code
			}
		case "Panic":
			a.Problems = append(a.Problems, "audit "+l.AuditID+" stage Panic")
		}
	}
	var entries []*entry
	for _, e := range byID {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].order < entries[j].order })
	for _, e := range entries {
		switch {
		case e.r.Verb == "watch" && e.complete && !e.started:
			a.Problems = append(a.Problems, "watch "+e.r.AuditID+" ends without its ResponseStarted line")
		case e.r.Verb == "watch" && !e.complete:
			a.Problems = append(a.Problems, "watch "+e.r.AuditID+" has no ResponseComplete line")
		case e.r.Verb != "watch" && !e.complete:
			a.Problems = append(a.Problems, e.r.Verb+" "+e.r.AuditID+" has no ResponseComplete line")
		default:
			a.Requests = append(a.Requests, e.r)
		}
	}
	sort.SliceStable(a.Requests, func(i, j int) bool { return a.Requests[i].Received.Before(a.Requests[j].Received) })
	return a
}

// Span is a closed time interval.
type Span struct {
	Start, End time.Time
}

// Holds reports whether t lies in the span.
func (s Span) Holds(t time.Time) bool { return !t.Before(s.Start) && !t.After(s.End) }

// Seconds is the span length in whole seconds.
func (s Span) Seconds() int64 { return int64(s.End.Sub(s.Start) / time.Second) }

// query returns one query parameter of a request URI ("" when absent).
func query(uri, name string) string {
	_, q, _ := strings.Cut(uri, "?")
	v, err := url.ParseQuery(q)
	if err != nil {
		return ""
	}
	return v.Get(name)
}

// in returns the requests of user received inside span with one of the verbs.
func in(reqs []Request, user string, span Span, verbs ...string) []Request {
	var out []Request
	for _, r := range reqs {
		if r.User != user || !span.Holds(r.Received) {
			continue
		}
		for _, v := range verbs {
			if r.Verb == v {
				out = append(out, r)
				break
			}
		}
	}
	return out
}
