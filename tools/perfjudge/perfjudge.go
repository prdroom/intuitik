// Package perfjudge is the judge of the server performance harness of packet FA2-T12
// (section 6.4): a pure package, with no network, no clock and no environment, in the
// pattern of tools/hk2judge. Judge reads the recorded inputs of one scale in the
// measurement cluster (the generator's fixture table, the read-back counts, the served
// resources, the RSS samples and VmHWM of each run, the audit log lines, the call and
// noisy window bounds, the control client's cause records and the graph answer bodies);
// JudgeRepro reads those of the 410 reproduction cluster (the audit log lines, the bounds
// of windows 3a and 3b and the cause records). Each prints one PERF line per measured
// quantity with its raw values, the computed value and PASS or FAIL, and every line ends
// with the cluster it judged (packet 6.2 r5.1). Exit 0 when every verdict passes, 1 for a
// measured FAIL, 2 for an ERROR (incomplete input, unattributed or orphan audit lines,
// counts not equal to the fixture).
package perfjudge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Input is everything one scale recorded in the measurement cluster.
type Input struct {
	Cluster      string
	Scale        string
	Fixture      []Count
	Readback     []Count
	Served       []Resource
	PanelUser    string
	ControlUser  string
	Measured     string // the measured namespace: S perf-001, L perf-big
	MeasuredPods int    // the fixture's Pods of the measured namespace
	Runs         []Run
	Noisy        Window // C6 window (2); zero at XL
	Audit        [][]byte
	Records      [][]byte
	Files        []File
}

// Run is one run of the binary: its graph calls, its product idle window (zero at XL), the
// RSS samples taken every Interval and the VmHWM read after the window.
type Run struct {
	Index    int
	Calls    []Call
	Idle     Span
	Samples  []int64
	Interval time.Duration
	HWM      int64
}

// Call is one graph call: "cluster" or the namespace name, its bounds, HTTP status and body.
type Call struct {
	Name   string
	Span   Span
	Status int
	Body   []byte
}

// Window is one control window and the client's list interval in it (noisy window only).
type Window struct {
	Span     Span
	Interval time.Duration
}

// File is a file the judge prints the byte count and sha256 of.
type File struct {
	Name string
	Data []byte
}

// Report is the judge's output: the lines, the exit code and the values the harness
// checks.
type Report struct {
	Lines         []string
	Exit          int
	ScaleOK       bool
	Peaks         []int64
	C5            []SetResult
	Noisy         Counts
	NoisyExpected int
	Positive      Counts
	Mixed         Counts
	Errors        []string
	Failed        []string
}

func (r *Report) printf(format string, a ...any) {
	r.Lines = append(r.Lines, fmt.Sprintf(format, a...))
}

func (r *Report) errorf(format string, a ...any) {
	e := fmt.Sprintf(format, a...)
	r.Errors = append(r.Errors, e)
	r.Lines = append(r.Lines, "PERF ERROR "+e)
}

// ReproInput is everything the 410 reproduction cluster recorded: C6 windows (3a) and (3b).
type ReproInput struct {
	Cluster     string
	PanelUser   string
	ControlUser string
	Positive    Window // C6 window (3a)
	Mixed       Window // C6 window (3b)
	Audit       [][]byte
	Records     [][]byte
}

// label ends every line of the report with the cluster it judged.
func (r *Report) label(cluster string) {
	for i, l := range r.Lines {
		r.Lines[i] = l + " cluster=" + cluster
	}
}

// verdict sets the exit code and prints the last line.
func (r *Report) verdict() {
	switch {
	case len(r.Errors) > 0:
		r.Exit = 2
		r.printf("PERF verdict=ERROR %d error(s)", len(r.Errors))
	case len(r.Failed) > 0:
		r.Exit = 1
		r.printf("PERF verdict=FAIL %s", strings.Join(r.Failed, " "))
	default:
		r.printf("PERF verdict=PASS")
	}
}

// audit parses the audit lines and the cause records of one cluster, prints their summary
// and records every problem as an ERROR, a cause record of another cluster included.
func (r *Report) audit(cluster, panelUser, controlUser string, lines, recordLines [][]byte) (Audit, []Record) {
	audit := ParseAudit(cluster, lines)
	records, recordProblems := ParseRecords(recordLines)
	panel, control := 0, 0
	for _, q := range audit.Requests {
		switch q.User {
		case panelUser:
			panel++
		case controlUser:
			control++
		}
	}
	r.printf("PERF audit lines=%d requests=%d duplicates=%d panel=%s:%d control=%s:%d records=%d", audit.Lines, len(audit.Requests), len(audit.Duplicates), panelUser, panel, controlUser, control, len(records))
	for _, d := range audit.Duplicates {
		r.printf("PERF audit duplicate %s (printed, not counted)", d)
	}
	for _, p := range append(audit.Problems, recordProblems...) {
		r.errorf("audit %s", p)
	}
	for _, rec := range records {
		if rec.Cluster != cluster {
			r.errorf("cause record %s of cluster %s in the input of cluster %s", rec.AuditID, rec.Cluster, cluster)
		}
	}
	return audit, records
}

// JudgeRepro judges the 410 reproduction cluster: C6 windows (3a) and (3b) of the control
// identity; a panel request inside either window is unattributed.
func JudgeRepro(in ReproInput) Report {
	var r Report
	audit, records := r.audit(in.Cluster, in.PanelUser, in.ControlUser, in.Audit, in.Records)
	for _, w := range []struct {
		name string
		w    Window
		out  *Counts
	}{{"410-positive", in.Positive, &r.Positive}, {"410-mixed", in.Mixed, &r.Mixed}} {
		if w.w.Span.End.IsZero() {
			r.errorf("control window %s has no bounds", w.name)
			continue
		}
		for _, q := range audit.Requests {
			if q.User == in.PanelUser && w.w.Span.Holds(q.Received) {
				r.errorf("unattributed panel request %s %s inside a control window", q.AuditID, q.Verb)
			}
		}
		c, detail := Classify(audit.Requests, in.ControlUser, w.w.Span, records)
		*w.out = c
		for _, d := range detail {
			r.printf("PERF idle window=%s %s", w.name, d)
		}
		r.printf("PERF C6 window=%s length=%d relist-410=%d unverified=%d periodic=%d result=%s", w.name, w.w.Span.Seconds(), c.Relist410, c.Unverified, c.Periodic, passFail(c.Pass()))
	}
	r.verdict()
	r.label(in.Cluster)
	return r
}

// Judge judges one scale in the measurement cluster.
func Judge(in Input) Report {
	var r Report
	sc := JudgeScale(in.Scale, in.Fixture, in.Readback)
	r.Lines = append(r.Lines, sc.Lines...)
	r.Errors = append(r.Errors, sc.Errors...)
	r.ScaleOK = sc.OK

	audit, records := r.audit(in.Cluster, in.PanelUser, in.ControlUser, in.Audit, in.Records)

	// Attribution (6.3): a panel request inside a control window, or a control request
	// inside a call or product window, is unattributed.
	var product, controlSpans []Span
	for _, run := range in.Runs {
		for _, c := range run.Calls {
			product = append(product, c.Span)
		}
		if !run.Idle.End.IsZero() {
			product = append(product, run.Idle)
		}
	}
	if !in.Noisy.Span.End.IsZero() {
		controlSpans = append(controlSpans, in.Noisy.Span)
	}
	for _, q := range audit.Requests {
		for _, s := range controlSpans {
			if q.User == in.PanelUser && s.Holds(q.Received) {
				r.errorf("unattributed panel request %s %s inside a control window", q.AuditID, q.Verb)
			}
		}
		for _, s := range product {
			if q.User == in.ControlUser && s.Holds(q.Received) {
				r.errorf("unattributed control request %s %s inside a product window", q.AuditID, q.Verb)
			}
		}
	}

	// Calls of every run: status and duration.
	for _, run := range in.Runs {
		for _, c := range run.Calls {
			r.printf("PERF call run=%d name=%s status=%d bytes=%d ms=%d", run.Index, c.Name, c.Status, len(c.Body), c.Span.End.Sub(c.Span.Start).Milliseconds())
			if c.Status != 200 && in.Scale != "XL" {
				r.errorf("run %d call %s status %d", run.Index, c.Name, c.Status)
			}
		}
	}

	// N-5 (3.2): the run peaks.
	for _, run := range in.Runs {
		lo, hi := int64(0), int64(0)
		for i, s := range run.Samples {
			if i == 0 || s < lo {
				lo = s
			}
			if s > hi {
				hi = s
			}
		}
		peak := RunPeak(run.Samples, run.HWM)
		r.Peaks = append(r.Peaks, peak)
		r.printf("PERF rss run=%d samples=%d interval=%s min=%d max=%d vmhwm=%d peak=%d (%s)", run.Index, len(run.Samples), run.Interval, lo, hi, run.HWM, peak, MB(peak))
	}
	n5 := JudgeN5(r.Peaks)
	switch {
	case in.Scale == "L" && !r.ScaleOK:
		r.printf("PERF N-5 not-measured (scale line not ok)")
	case in.Scale == "L" && len(r.Peaks) != N5Runs:
		r.errorf("N-5 needs %d L runs, got %d", N5Runs, len(r.Peaks))
	case in.Scale == "L":
		r.printf("%s", n5.Line("L"))
		if !n5.Pass {
			r.Failed = append(r.Failed, "N-5")
		}
	default:
		r.printf("%s", n5.Line(in.Scale))
	}

	if in.Scale != "XL" && len(in.Runs) > 0 {
		r.judgeReads(in, audit.Requests, records)
	}

	for _, f := range in.Files {
		sum := sha256.Sum256(f.Data)
		r.printf("PERF file %s bytes=%d sha256=%s", f.Name, len(f.Data), hex.EncodeToString(sum[:]))
	}

	r.verdict()
	r.label(in.Cluster)
	return r
}

// judgeReads prints C5, the N-6 read pattern of the cluster call and the measured
// namespace call of run 1, the product idle window of every run and the noisy window.
func (r *Report) judgeReads(in Input, reqs []Request, records []Record) {
	run := in.Runs[0]
	var cluster, measured *Call
	for i := range run.Calls {
		switch run.Calls[i].Name {
		case "cluster":
			cluster = &run.Calls[i]
		case in.Measured:
			measured = &run.Calls[i]
		}
	}
	if cluster == nil || measured == nil {
		r.errorf("run 1 lacks the cluster call or the call of %s", in.Measured)
		return
	}
	podNodes, err := CountPodNodes(measured.Body)
	if err != nil {
		r.errorf("the answer of %s does not decode: %v", in.Measured, err)
	}
	r.printf("PERF N-6 call=%s pod-nodes=%d fixture-pods=%d", in.Measured, podNodes, in.MeasuredPods)
	for _, c := range []struct {
		call    *Call
		cluster bool
	}{{cluster, true}, {measured, false}} {
		expected := ExpectedSet(c.cluster, in.Served, true)
		var names []string
		for _, e := range expected {
			names = append(names, e.Group+"/"+e.Version+"/"+e.Resource)
		}
		r.printf("PERF C5 call=%s expected=%s", c.call.Name, strings.Join(names, ","))
		set := JudgeIdentity(c.call.Name, reqs, in.PanelUser, c.call.Span, expected)
		r.C5 = append(r.C5, set)
		r.printf("%s", set.Line())
		if !set.Pass {
			r.Failed = append(r.Failed, "C5:"+c.call.Name)
		}
		for _, p := range JudgePages(c.call.Name, reqs, in.PanelUser, c.call.Span, expected, !c.cluster, in.MeasuredPods, podNodes) {
			if !r.ScaleOK {
				r.printf("PERF N-6 call=%s resource=%s not-measured (scale line not ok)", p.Call, p.Resource)
				continue
			}
			r.printf("%s", p.Line())
			if !p.Pass {
				r.Failed = append(r.Failed, "N-6:"+p.Call+":"+p.Resource)
			}
		}
	}
	for _, run := range in.Runs {
		if run.Idle.End.IsZero() {
			continue
		}
		c, detail := Classify(reqs, in.PanelUser, run.Idle, records)
		for _, d := range detail {
			r.printf("PERF idle window=product run=%d %s", run.Index, d)
		}
		r.printf("PERF N-6 idle window=product run=%d length=%d %s info=%s judged-in=T11", run.Index, run.Idle.Seconds(), c, passFail(c.Pass()))
	}
	w := in.Noisy
	if w.Span.End.IsZero() {
		r.errorf("control window noisy has no bounds")
		return
	}
	c, detail := Classify(reqs, in.ControlUser, w.Span, records)
	r.Noisy = c
	for _, d := range detail {
		r.printf("PERF idle window=noisy %s", d)
	}
	r.NoisyExpected = ExpectedPeriodic(w.Span.End.Sub(w.Span.Start), w.Interval)
	r.printf("PERF C6 window=noisy length=%d interval=%s expected=%d %s result=%s", w.Span.Seconds(), w.Interval, r.NoisyExpected, c, passFail(c.Pass()))
}
