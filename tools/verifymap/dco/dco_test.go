package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// unitCase is one judging vector of testdata/unit_cases.json. Addresses live only in
// testdata (packet M1-T1 criterion 2).
type unitCase struct {
	Name        string `json:"name"`
	AuthorEmail string `json:"author_email"`
	Message     string `json:"message"`
	Want        string `json:"want"`
}

func TestJudge(t *testing.T) {
	raw, err := os.ReadFile("testdata/unit_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []unitCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("testdata/unit_cases.json holds no cases")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			ok, reason := Judge(c.AuthorEmail, c.Message)
			got := reason
			if ok {
				got = "pass"
			}
			if got != c.Want {
				t.Fatalf("Judge = %q, want %q", got, c.Want)
			}
			if ok && reason != "" {
				t.Fatalf("a passing commit has reason %q", reason)
			}
		})
	}
}

func TestParseCommit(t *testing.T) {
	raw, err := os.ReadFile("testdata/raw_commit.txt")
	if err != nil {
		t.Fatal(err)
	}
	// The checkout may convert line endings; git cat-file output always uses LF.
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	var ids map[string]string
	idsRaw, err := os.ReadFile("testdata/gitrange.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(idsRaw, &ids); err != nil {
		t.Fatal(err)
	}

	c, err := ParseCommit([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthorEmail != ids["author_email"] {
		t.Errorf("AuthorEmail = %q, want %q", c.AuthorEmail, ids["author_email"])
	}
	if c.Parents != 2 {
		t.Errorf("Parents = %d, want 2", c.Parents)
	}
	wantMessage := "Merge branch side\n\n" + ids["signoff"] + "\n"
	if c.Message != wantMessage {
		t.Errorf("Message = %q, want %q", c.Message, wantMessage)
	}
}

func TestParseCommitWithoutAuthor(t *testing.T) {
	if _, err := ParseCommit([]byte("tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\n\nsubject\n")); err == nil {
		t.Fatal("ParseCommit accepted a commit without an author header")
	}
}

func TestValidSHA(t *testing.T) {
	for in, want := range map[string]bool{
		"60559eaf4cae21932f34ffac8f91abbceeb8d4f9": true,
		"60559EAF4CAE21932F34FFAC8F91ABBCEEB8D4F9": true,
		"60559eaf": false,
		"60559eaf4cae21932f34ffac8f91abbceeb8d4f9a": false,
		"g0559eaf4cae21932f34ffac8f91abbceeb8d4f9":  false,
		"": false,
	} {
		if got := validSHA(in); got != want {
			t.Errorf("validSHA(%q) = %v, want %v", in, got, want)
		}
	}
}
