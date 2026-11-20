package sampler

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prdroom/intuitik/tools/perfjudge"
)

// block is the size the helper process allocates and holds (packet 6.4 C3).
const block = 300_000_000

// TestMain runs the helper process when the test binary is re-executed as one: it prints
// its own VmRSS before the allocation, allocates and touches 300 MB, prints "ready" and
// holds the block until its stdin closes.
func TestMain(m *testing.M) {
	if os.Getenv("PERFJUDGE_SAMPLER_HELPER") == "1" {
		st, err := ReadStatus(os.Getpid())
		fmt.Printf("baseline %d %v\n", st.RSS, err)
		buf := make([]byte, block)
		for i := 0; i < len(buf); i += 4096 {
			buf[i] = 1
		}
		fmt.Println("ready")
		io.Copy(io.Discard, os.Stdin)
		runtime.KeepAlive(buf)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRSSSampler(t *testing.T) {
	// The parser reads VmRSS, not VmSize, in bytes.
	st, err := ParseStatus([]byte("Name:\tx\nVmPeak:\t 1300000 kB\nVmSize:\t 1234567 kB\nVmHWM:\t  310000 kB\nVmRSS:\t  300000 kB\nThreads:\t5\n"))
	if err != nil || st.RSS != 300000*1024 || st.HWM != 310000*1024 || st.Size != 1234567*1024 {
		t.Errorf("ParseStatus: %+v %v, want RSS 307200000, HWM 317440000, Size 1264196608", st, err)
	}
	if _, err := ParseStatus([]byte("VmRSS:\t 1 kB\n")); err == nil {
		t.Error("ParseStatus without VmHWM and VmSize: no error")
	}

	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "PERFJUDGE_SAMPLER_HELPER=1")
	cmd.WaitDelay = 5 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		cmd.Wait()
	})
	sc := bufio.NewScanner(stdout)
	var baseline int64
	for sc.Scan() {
		line := sc.Text()
		t.Logf("helper: %s", line)
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "baseline" {
			baseline, _ = strconv.ParseInt(f[1], 10, 64)
		}
		if line == "ready" {
			break
		}
	}
	pid := cmd.Process.Pid
	s := Start(pid, 200*time.Millisecond)
	time.Sleep(1500 * time.Millisecond)
	samples, errs := s.Stop()
	hwm, err := ReadStatus(pid)
	if err != nil || len(errs) != 0 {
		t.Errorf("reads of the helper: %v %q", err, errs)
	}
	peak := perfjudge.RunPeak(samples, hwm.HWM)
	lo, hi := int64(block), int64(block)+baseline+20_000_000
	t.Logf("C3 helper pid=%d baseline=%d samples=%d %v VmHWM=%d VmSize=%d peak=%d band=[%d, %d]", pid, baseline, len(samples), samples, hwm.HWM, hwm.Size, peak, lo, hi)
	if len(samples) < 5 || peak < lo || peak > hi || baseline <= 0 {
		t.Errorf("C3 peak %d bytes (baseline %d, %d samples), want at least 5 samples and the peak in [%d, %d]", peak, baseline, len(samples), lo, hi)
	}
	// The VmRSS samples on their own, apart from VmHWM, show which process was read
	// (decision C3-BAND-T12 (d)): the helper's lie in the band.
	samplesInBand := func(s []int64) (int64, bool) {
		m := perfjudge.RunPeak(s, 0)
		return m, len(s) > 0 && m >= lo && m <= hi
	}
	if m, ok := samplesInBand(samples); !ok {
		t.Errorf("C3 helper VmRSS samples on their own: max %d bytes, want in [%d, %d]", m, lo, hi)
	}
	// Another process (this test, which holds no block) read in place of the helper gives a
	// peak outside the band, and its samples stay outside it beside the helper's VmHWM.
	own := Start(os.Getpid(), 200*time.Millisecond)
	time.Sleep(500 * time.Millisecond)
	ownSamples, _ := own.Stop()
	ownPeak := perfjudge.RunPeak(ownSamples, 0)
	if ownPeak <= 0 || ownPeak >= lo {
		t.Errorf("C3 another process: peak %d bytes, want above 0 and below the band [%d, %d]", ownPeak, lo, hi)
	}
	m, ok := samplesInBand(ownSamples)
	t.Logf("C3 wrong pid=%d samples=%d max=%d with helper VmHWM=%d: peak=%d samples-in-band=%v", os.Getpid(), len(ownSamples), m, hwm.HWM, perfjudge.RunPeak(ownSamples, hwm.HWM), ok)
	if ok {
		t.Errorf("C3 another process beside the helper's VmHWM %d: samples max %d bytes in the band [%d, %d], want outside", hwm.HWM, m, lo, hi)
	}
}
