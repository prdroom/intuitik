package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"k8s.io/klog/v2"
)

// captureStderr runs f with os.Stderr replaced by a pipe and returns what was written.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	f()
	os.Stderr = saved
	w.Close()
	out, _ := io.ReadAll(r)
	r.Close()
	return string(out)
}

// TestE13_KlogDiscarded (packet M1-T3b E13): after the product's setup, a klog.Errorf
// writes nothing to stderr. Positive control: before the setup the same call writes.
func TestE13_KlogDiscarded(t *testing.T) {
	const marker = "intuitik-klog-marker"
	before := captureStderr(t, func() {
		klog.Errorf("%s before the setup", marker)
		klog.Flush()
	})
	discardLibraryLogs()
	after := captureStderr(t, func() {
		klog.Errorf("%s after the setup", marker)
		klog.Flush()
	})
	t.Logf("E13 positive control before the setup: marker on stderr %d times; after the setup: %d times (%d bytes)",
		strings.Count(before, marker), strings.Count(after, marker), len(after))
	if !strings.Contains(before, marker) {
		t.Errorf("E13 positive control: klog wrote no marker to stderr before the setup (%q)", before)
	}
	if after != "" {
		t.Errorf("E13: klog wrote %q to stderr after the setup, want nothing", after)
	}
}
