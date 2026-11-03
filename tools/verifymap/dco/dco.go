package main

import (
	"regexp"
	"strings"
)

// Failure reasons printed in `DCO FAIL <sha> <reason>` lines.
const (
	reasonMissing   = "missing-signoff"
	reasonMismatch  = "signoff-author-mismatch"
	reasonMalformed = "malformed-signoff"
)

var (
	signoffKey  = regexp.MustCompile(`(?i)^signed-off-by:`)
	signoffLine = regexp.MustCompile(`(?i)^signed-off-by:[ \t]*(.*?)[ \t]*<([^<>]*)>[ \t]*$`)
)

// Judge applies the standard DCO rule to one non-merge commit: the trailer paragraph
// at the end of the message must hold at least one `Signed-off-by: <name> <email>`
// line whose email equals the author email, ignoring letter case. The name and the
// address form are not checked. It returns ok, or the failure reason.
//
// Without a matching sign-off, a sign-off key without an `<email>` gives
// malformed-signoff; otherwise well-formed sign-offs give signoff-author-mismatch,
// and no sign-off key gives missing-signoff.
func Judge(authorEmail, message string) (bool, string) {
	malformed, wellFormed := false, false
	for _, line := range trailerParagraph(message) {
		if !signoffKey.MatchString(line) {
			continue
		}
		m := signoffLine.FindStringSubmatch(line)
		if m == nil || strings.TrimSpace(m[2]) == "" {
			malformed = true
			continue
		}
		wellFormed = true
		if strings.EqualFold(strings.TrimSpace(m[2]), authorEmail) {
			return true, ""
		}
	}
	switch {
	case malformed:
		return false, reasonMalformed
	case wellFormed:
		return false, reasonMismatch
	default:
		return false, reasonMissing
	}
}

// trailerParagraph returns the lines of the last paragraph of the message, with CRLF
// read as LF. A message of one paragraph is a subject and body only and has no
// trailer paragraph.
func trailerParagraph(message string) []string {
	text := strings.ReplaceAll(message, "\r\n", "\n")
	var paragraphs [][]string
	var current []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			if len(current) > 0 {
				paragraphs = append(paragraphs, current)
				current = nil
			}
			continue
		}
		current = append(current, strings.TrimRight(line, " \t"))
	}
	if len(current) > 0 {
		paragraphs = append(paragraphs, current)
	}
	if len(paragraphs) < 2 {
		return nil
	}
	return paragraphs[len(paragraphs)-1]
}
