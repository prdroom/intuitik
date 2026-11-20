package perfjudge

import (
	"fmt"
	"strconv"
	"strings"
)

// N5Limit is N-5's backend RSS limit, read as 600,000,000 bytes (packet 3.2).
const N5Limit = 600_000_000

// N5Runs is the number of L runs whose maximum N-5 judges (PRD line 194).
const N5Runs = 5

// RunPeak is the peak RSS of one run: the maximum of the 1 s samples and the VmHWM read
// after the window (packet 6.3).
func RunPeak(samples []int64, hwm int64) int64 {
	peak := hwm
	for _, s := range samples {
		if s > peak {
			peak = s
		}
	}
	return peak
}

// RSSBand returns the C3 band problems of one live run (packet 6.3, 6.4): the VmHWM read
// error, the sample read errors, no sample, a sample at or below zero, and a VmHWM below
// 5,000,000 bytes. No problem is the band PASS. A sample above VmHWM is no problem: the run
// peak is max(VmHWM, samples) (decision C3-BAND-T12).
func RSSBand(run int, samples []int64, hwm int64, hwmErr error, sampleErrs []string) []string {
	var problems []string
	if hwmErr != nil {
		problems = append(problems, fmt.Sprintf("run %d VmHWM read: %v", run, hwmErr))
	}
	for _, e := range sampleErrs {
		problems = append(problems, fmt.Sprintf("run %d sample read: %s", run, e))
	}
	if len(samples) == 0 {
		problems = append(problems, fmt.Sprintf("run %d: no sample", run))
	}
	for i, v := range samples {
		if v <= 0 {
			problems = append(problems, fmt.Sprintf("run %d sample %d = %d bytes, not above 0", run, i+1, v))
			break
		}
	}
	if hwm < 5_000_000 {
		problems = append(problems, fmt.Sprintf("run %d VmHWM %d bytes below 5000000", run, hwm))
	}
	return problems
}

// N5Result is the N-5 judgement of the run peaks.
type N5Result struct {
	Peaks []int64
	Max   int64
	Pass  bool
}

// JudgeN5 judges the run peaks: PASS only when their maximum is at most 600,000,000 bytes.
func JudgeN5(peaks []int64) N5Result {
	r := N5Result{Peaks: peaks}
	for _, p := range peaks {
		if p > r.Max {
			r.Max = p
		}
	}
	r.Pass = len(peaks) > 0 && r.Max <= N5Limit
	return r
}

// MB prints a byte count in units of 1,000,000 bytes, exactly: 601000000 -> "601MB",
// 600000001 -> "600.000001MB".
func MB(n int64) string {
	whole, frac := n/1_000_000, n%1_000_000
	if frac == 0 {
		return strconv.FormatInt(whole, 10) + "MB"
	}
	return strings.TrimRight(fmt.Sprintf("%d.%06d", whole, frac), "0") + "MB"
}

// Line prints the N-5 line; at a scale other than L it is information (the limit applies
// to L, packet 6.5).
func (r N5Result) Line(scale string) string {
	var ps []string
	for _, p := range r.Peaks {
		ps = append(ps, MB(p))
	}
	runs := strings.Join(ps, ",")
	if scale == "L" {
		return fmt.Sprintf("PERF N-5 runs=%s max=%s limit=600MB result=%s", runs, MB(r.Max), passFail(r.Pass))
	}
	return fmt.Sprintf("PERF N-5 scale=%s runs=%s max=%s limit=600MB info=%s (the limit applies to L)", scale, runs, MB(r.Max), passFail(r.Pass))
}
