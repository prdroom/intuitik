package main

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var (
	shaPattern  = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	authorField = regexp.MustCompile(`^author .*<([^<>]*)> [0-9]+ [+-][0-9]{4}$`)
)

func validSHA(s string) bool { return shaPattern.MatchString(s) }

// Commit holds the fields of `git cat-file commit` output that the check uses.
type Commit struct {
	AuthorEmail string
	Parents     int
	Message     string
}

// ParseCommit reads raw `git cat-file commit` output: header lines up to the first
// empty line (continuation lines of multi-line headers start with a space), then the
// message. Mailmap is not applied.
func ParseCommit(raw []byte) (Commit, error) {
	text := string(raw)
	header, message, found := strings.Cut(text, "\n\n")
	if !found {
		header, message = strings.TrimSuffix(text, "\n"), ""
	}
	var c Commit
	author := false
	for _, line := range strings.Split(header, "\n") {
		switch {
		case strings.HasPrefix(line, "parent "):
			c.Parents++
		case strings.HasPrefix(line, "author "):
			m := authorField.FindStringSubmatch(line)
			if m == nil {
				return Commit{}, fmt.Errorf("unreadable author header %q", line)
			}
			c.AuthorEmail = m[1]
			author = true
		}
	}
	if !author {
		return Commit{}, errors.New("commit has no author header")
	}
	c.Message = message
	return c, nil
}

// git runs one git command in the current directory and returns its stdout.
func git(args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// commitRange lists `git rev-list --topo-order base..head` after checking that both
// ends name existing commits.
func commitRange(base, head string) ([]string, error) {
	for _, sha := range []string{base, head} {
		if _, err := git("cat-file", "-e", sha+"^{commit}"); err != nil {
			return nil, fmt.Errorf("%s is not a commit in this repository: %v", sha, err)
		}
	}
	out, err := git("rev-list", "--topo-order", base+".."+head)
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

func readCommit(sha string) (Commit, error) {
	raw, err := git("cat-file", "commit", sha)
	if err != nil {
		return Commit{}, err
	}
	return ParseCommit(raw)
}
