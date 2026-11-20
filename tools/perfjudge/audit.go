package perfjudge

import (
	"net/url"
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
	// Red stub of packet FA2-T12 6.5: PASS with zero counts; the sampler returns 0.
	return Audit{}
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
