package main

import "testing"

func TestParseNvmrc(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"26.13.0\n", "26.13.0", true},
		{"v26.13.0\n", "26.13.0", true},
		{"26.13.0", "26.13.0", true},
		{"26.13.0\r\n", "26.13.0", true},
		{"26\n", "", false},
		{"lts/*\n", "", false},
		{"26.13\n", "", false},
		{"", "", false},
	} {
		got, err := parseNvmrc(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("parseNvmrc(%q) = %q, %v; want %q, ok=%v", c.in, got, err, c.want, c.ok)
		}
	}
}

func TestParseToolVersion(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"v26.13.0\n", "26.13.0", true},
		{"11.20.0\n", "11.20.0", true},
		{"", "", false},
		{"v26\n", "", false},
	} {
		got, err := parseToolVersion(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("parseToolVersion(%q) = %q, %v; want %q, ok=%v", c.in, got, err, c.want, c.ok)
		}
	}
}

const sampleIndex = `[
  {"version":"v27.0.0-alpha.0.0.0","npm":"12.1.0","lts":false},
  {"version":"v26.13.0","npm":"11.20.0","lts":"Lithium"},
  {"version":"v26.12.1","npm":"11.19.0","lts":false}
]`

func TestNPMForNode(t *testing.T) {
	got, err := npmForNode([]byte(sampleIndex), "26.13.0")
	if err != nil || got != "11.20.0" {
		t.Fatalf("npmForNode(26.13.0) = %q, %v; want 11.20.0", got, err)
	}
	if got, err := npmForNode([]byte(sampleIndex), "26.12.1"); err != nil || got != "11.19.0" {
		t.Fatalf("npmForNode(26.12.1) = %q, %v; want 11.19.0", got, err)
	}
	if _, err := npmForNode([]byte(sampleIndex), "26.1.0"); err == nil {
		t.Fatal("npmForNode accepted a version that is not in the index")
	}
	if _, err := npmForNode([]byte(`{"not":"a list"}`), "26.13.0"); err == nil {
		t.Fatal("npmForNode accepted an index that is not a list")
	}
	if _, err := npmForNode([]byte(`[{"version":"v26.13.0","npm":""}]`), "26.13.0"); err == nil {
		t.Fatal("npmForNode accepted an entry without an npm field")
	}
}

func TestCompareVersions(t *testing.T) {
	if !compareLine(nopWriter{}, "node", "26.13.0", "26.13.0", "web/.nvmrc") {
		t.Error("equal versions reported as different")
	}
	if compareLine(nopWriter{}, "npm", "11.20.0", "11.19.0", "index") {
		t.Error("different versions reported as equal")
	}
	if compareLine(nopWriter{}, "npm", "11.20.0", "11.20.0 ", "index") {
		t.Error("a near match was reported as equal; the comparison must be exact")
	}
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
