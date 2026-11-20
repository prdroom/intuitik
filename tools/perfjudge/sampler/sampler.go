// Package sampler is the RSS sampler of the server performance harness (packet FA2-T12
// 6.3, C3): VmRSS of one process from /proc/<pid>/status at a fixed interval, and VmHWM
// once after the window. It is apart from tools/perfjudge, which reads no environment;
// the judge receives the samples.
package sampler

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Status is the memory part of /proc/<pid>/status, in bytes.
type Status struct {
	RSS  int64 // VmRSS: resident set size now
	HWM  int64 // VmHWM: peak resident set size
	Size int64 // VmSize: virtual size, never a sample
}

// ParseStatus reads VmRSS, VmHWM and VmSize (kB) from the text of /proc/<pid>/status.
func ParseStatus(b []byte) (Status, error) {
	var s Status
	found := 0
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		var dst *int64
		switch name {
		case "VmRSS":
			dst = &s.RSS
		case "VmHWM":
			dst = &s.HWM
		case "VmSize":
			dst = &s.Size
		default:
			continue
		}
		f := strings.Fields(rest)
		if len(f) != 2 || f[1] != "kB" {
			return Status{}, fmt.Errorf("%s: unexpected value %q", name, rest)
		}
		kb, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			return Status{}, fmt.Errorf("%s: %v", name, err)
		}
		*dst = kb * 1024
		found++
	}
	if found != 3 {
		return Status{}, fmt.Errorf("VmRSS, VmHWM and VmSize: %d of 3 found", found)
	}
	return s, nil
}

// ReadStatus reads the memory status of process pid.
func ReadStatus(pid int) (Status, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return Status{}, err
	}
	return ParseStatus(b)
}

// Sampler samples VmRSS of one process at a fixed interval until Stop.
type Sampler struct {
	pid      int
	interval time.Duration
	stop     chan struct{}
	done     chan struct{}
	mu       sync.Mutex
	samples  []int64
	errors   []string
}

// Start begins sampling pid every interval; the first sample is taken at once.
func Start(pid int, interval time.Duration) *Sampler {
	s := &Sampler{pid: pid, interval: interval, stop: make(chan struct{}), done: make(chan struct{})}
	go s.loop()
	return s
}

func (s *Sampler) loop() {
	defer close(s.done)
	tick := time.NewTicker(s.interval)
	defer tick.Stop()
	for {
		st, err := ReadStatus(s.pid)
		s.mu.Lock()
		if err != nil {
			s.errors = append(s.errors, err.Error())
		} else {
			s.samples = append(s.samples, st.RSS)
		}
		s.mu.Unlock()
		select {
		case <-s.stop:
			return
		case <-tick.C:
		}
	}
}

// Stop ends sampling and returns the samples (bytes) and the read errors.
func (s *Sampler) Stop() ([]int64, []string) {
	close(s.stop)
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.samples...), append([]string(nil), s.errors...)
}

// Interval is the sampling interval.
func (s *Sampler) Interval() time.Duration { return s.interval }
