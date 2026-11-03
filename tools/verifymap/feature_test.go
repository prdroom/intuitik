package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Drive of embedded-page is read from its map file, so the map states the
// inputs and expected outputs that the runner checks (packet M1-T1 criterion 6).
func TestEmbeddedPageDrive(t *testing.T) {
	f, err := ParseFeature(filepath.Join("..", "..", "verify", "features", "embedded-page.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := Drive{
		Harness:      "http-get",
		Path:         "/",
		Status:       200,
		ContentType:  "text/html",
		BodyContains: "intuitik-embedded-page",
	}
	if f.ID != "embedded-page" || f.Drive != want {
		t.Fatalf("ParseFeature = %+v, want ID embedded-page and Drive %+v", f, want)
	}
}

func TestParseDriveRejectsIncompleteBlock(t *testing.T) {
	for _, block := range []string{
		"path: /\nstatus: 200\ncontent-type: text/html\n",
		"path: /\nstatus: two hundred\ncontent-type: text/html\nbody-contains: x\n",
		"path: /\nstatus: 200\ncontent-type: text/html\nbody-contains: x\nmethod: POST\n",
		"path /\nstatus: 200\ncontent-type: text/html\nbody-contains: x\n",
	} {
		if _, err := parseDriveBlock("http-get", block); err == nil {
			t.Errorf("parseDriveBlock accepted %q", block)
		}
	}
}

// apache20SHA256 is the sha256 of https://www.apache.org/licenses/LICENSE-2.0.txt.
const apache20SHA256 = "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30"

// Criterion 3: LICENSE is the Apache-2.0 text; both values are printed.
func TestLicenseIsApache20(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	actual := hex.EncodeToString(sum[:])
	t.Logf("LICENSE sha256 actual=%s expected=%s (Apache-2.0, https://www.apache.org/licenses/LICENSE-2.0.txt)", actual, apache20SHA256)
	if actual != apache20SHA256 {
		t.Fatalf("LICENSE sha256 %s differs from the Apache-2.0 text %s", actual, apache20SHA256)
	}
}

// Criterion 3: NOTICE has the two section headings, in this order.
func TestNoticeSections(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "NOTICE"))
	if err != nil {
		t.Fatal(err)
	}
	var headings []string
	for _, l := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(l, "## ") {
			headings = append(headings, l)
		}
	}
	want := []string{
		"## Third-party code attribution",
		"## Kubernetes documentation (CC BY 4.0)",
	}
	if strings.Join(headings, "|") != strings.Join(want, "|") {
		t.Fatalf("NOTICE headings %q, want %q", headings, want)
	}
}
