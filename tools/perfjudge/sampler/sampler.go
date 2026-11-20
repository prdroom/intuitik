// Package sampler is the RSS sampler of the server performance harness (packet FA2-T12
// 6.3, C3): VmRSS of one process from /proc/<pid>/status at a fixed interval, and VmHWM
// once after the window. It is apart from tools/perfjudge, which reads no environment;
// the judge receives the samples.
package sampler

import (
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
	// Red stub of packet FA2-T12 6.5: PASS with zero counts; the sampler returns 0.
	return Status{}, nil
}

// ReadStatus reads the memory status of process pid.
func ReadStatus(pid int) (Status, error) {
	// Red stub of packet FA2-T12 6.5: PASS with zero counts; the sampler returns 0.
	return Status{}, nil
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
