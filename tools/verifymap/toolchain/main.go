// Command toolchain compares the Node and npm versions on PATH with their expected
// values (packet M1-T1 criterion 8), exactly:
//   - Node: the x.y.z in the -nvmrc file;
//   - npm: the `npm` field of that Node version's entry in the Node.js release index.
//
// It prints each expected and actual pair and exits 1 when either pair differs, 2
// when a value cannot be read.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const indexURL = "https://nodejs.org/dist/index.json"

var semver = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func main() {
	nvmrc := flag.String("nvmrc", "web/.nvmrc", "file holding the exact Node version x.y.z")
	flag.Parse()
	os.Exit(run(*nvmrc, os.Stdout))
}

func run(nvmrcPath string, out io.Writer) int {
	raw, err := os.ReadFile(nvmrcPath)
	if err != nil {
		fmt.Fprintf(out, "toolchain: ERROR %v\n", err)
		return 2
	}
	wantNode, err := parseNvmrc(string(raw))
	if err != nil {
		fmt.Fprintf(out, "toolchain: ERROR %s: %v\n", nvmrcPath, err)
		return 2
	}
	gotNode, err := toolVersion("node")
	if err != nil {
		fmt.Fprintf(out, "toolchain: ERROR %v\n", err)
		return 2
	}
	gotNPM, err := toolVersion("npm")
	if err != nil {
		fmt.Fprintf(out, "toolchain: ERROR %v\n", err)
		return 2
	}
	index, err := fetchIndex()
	if err != nil {
		fmt.Fprintf(out, "toolchain: ERROR %v\n", err)
		return 2
	}
	wantNPM, err := npmForNode(index, wantNode)
	if err != nil {
		fmt.Fprintf(out, "toolchain: ERROR %s: %v\n", indexURL, err)
		return 2
	}

	nodeOK := compareLine(out, "node", wantNode, gotNode, nvmrcPath)
	npmOK := compareLine(out, "npm", wantNPM, gotNPM, indexURL+" entry v"+wantNode)
	if !nodeOK || !npmOK {
		return 1
	}
	return 0
}

// parseNvmrc returns the exact x.y.z of an .nvmrc file; a leading "v" is allowed.
func parseNvmrc(s string) (string, error) {
	v := strings.TrimPrefix(strings.TrimSpace(s), "v")
	if !semver.MatchString(v) {
		return "", fmt.Errorf("want an exact version x.y.z, got %q", strings.TrimSpace(s))
	}
	return v, nil
}

// parseToolVersion reads the output of `node --version` or `npm --version`.
func parseToolVersion(s string) (string, error) {
	v := strings.TrimPrefix(strings.TrimSpace(s), "v")
	if !semver.MatchString(v) {
		return "", fmt.Errorf("unreadable version output %q", strings.TrimSpace(s))
	}
	return v, nil
}

func toolVersion(tool string) (string, error) {
	out, err := exec.Command(tool, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("%s --version: %v", tool, err)
	}
	return parseToolVersion(string(out))
}

func fetchIndex() ([]byte, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(indexURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", indexURL, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// npmForNode returns the npm field of the index entry whose version is v<node>.
func npmForNode(index []byte, node string) (string, error) {
	var entries []struct {
		Version string `json:"version"`
		NPM     string `json:"npm"`
	}
	if err := json.Unmarshal(index, &entries); err != nil {
		return "", fmt.Errorf("index is not a list of releases: %v", err)
	}
	for _, e := range entries {
		if e.Version == "v"+node {
			if e.NPM == "" {
				return "", fmt.Errorf("entry v%s has no npm field", node)
			}
			return e.NPM, nil
		}
	}
	return "", errors.New("no entry v" + node)
}

// compareLine prints one expected/actual pair and reports whether they are equal,
// character for character.
func compareLine(out io.Writer, name, expected, actual, source string) bool {
	equal := expected == actual
	result := "ok"
	if !equal {
		result = "FAIL"
	}
	fmt.Fprintf(out, "toolchain: %s expected=%s actual=%s equal=%t (expected from %s) %s\n", name, expected, actual, equal, source, result)
	return equal
}
