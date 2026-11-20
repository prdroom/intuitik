package perfjudge

import (
	"encoding/json"
	"fmt"
	"time"
)

// Record is one line of the control client's cause records (packet 6.3): the cluster it
// talked to, the Audit-Id response header of every request it made, and for a watch the
// terminal event it received.
type Record struct {
	Cluster string       `json:"cluster"`
	AuditID string       `json:"auditID"`
	Verb    string       `json:"verb"`
	Path    string       `json:"path"`
	Event   *RecordEvent `json:"event,omitempty"`
}

// RecordEvent is the terminal event of a watch.
type RecordEvent struct {
	Type string `json:"type"`
	Code int    `json:"code"`
}

// ParseRecords reads the cause record lines; a line that does not decode with a cluster and
// an auditID is a problem.
func ParseRecords(lines [][]byte) ([]Record, []string) {
	var out []Record
	var problems []string
	for i, raw := range lines {
		if len(raw) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(raw, &r); err != nil || r.AuditID == "" || r.Cluster == "" {
			problems = append(problems, fmt.Sprintf("cause record line %d does not decode with a cluster and an auditID", i+1))
			continue
		}
		out = append(out, r)
	}
	return out, problems
}

// Counts are the classes of the lists of one window.
type Counts struct {
	Periodic, Relist410, Unverified int
}

func (c Counts) String() string {
	return fmt.Sprintf("periodic=%d relist-410=%d unverified=%d", c.Periodic, c.Relist410, c.Unverified)
}

// Pass is the detector's verdict on a window: no periodic and no unverified list.
func (c Counts) Pass() bool { return c.Periodic == 0 && c.Unverified == 0 }

// AfterWatch is the time after a watch end within which a list counts as following it.
const AfterWatch = 5 * time.Second

// Classify classes each list of user received inside span (packet 3.4, the C6 table of
// 6.3): relist-410 when it is bound to an unconsumed 410 record (the client recorded the
// list's auditID, and the record of a watch on the same cluster, group, resource,
// namespace and scope says its terminal event was ERROR 410, and the list follows that
// watch's end within 5 s; each record binds one list); else unverified when it follows the
// end of any watch on the same cluster, group, resource, namespace and scope within 5 s;
// else periodic. Records and requests are keyed by cluster and auditID, so a record of one
// cluster never binds a request of another. The detail lines name each list and its class.
func Classify(reqs []Request, user string, span Span, records []Record) (Counts, []string) {
	// Red stub of packet FA2-T12 6.5: PASS with zero counts; the sampler returns 0.
	return Counts{}, nil
}

// ExpectedPeriodic is the count of lists a client makes at a fixed interval inside a
// window (C6 noisy window), before the +-1 allowance for the window edges.
func ExpectedPeriodic(length, interval time.Duration) int {
	if interval <= 0 {
		return 0
	}
	return int(length / interval)
}
