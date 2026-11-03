package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Fixed H2 headings of a feature file, in order. The third one ends with the drive id
// of the harness: `Driving it with <drive id>`.
const (
	headingSubFeatures = "Sub-features"
	headingHowToGet    = "How to get to it (user POV)"
	drivingPrefix      = "Driving it with "
	headingGotchas     = "Gotchas"
)

// knownDrives are the drive ids the runner can execute.
var knownDrives = map[string]bool{
	"http-get": true,
}

// Drive is the executable part of a feature file: the fenced ```drive block inside
// the "Driving it with http-get" section.
//
//	path: /
//	status: 200
//	content-type: text/html
//	body-contains: <marker>
type Drive struct {
	Harness      string
	Path         string
	Status       int
	ContentType  string
	BodyContains string
}

// Feature is one verify/features/<feature-id>.md file.
type Feature struct {
	ID    string
	Drive Drive
}

// section is one H2 section of a markdown file; code fences are skipped when
// looking for headings.
type section struct {
	heading string
	body    []string
}

func splitSections(text string) []section {
	var sections []section
	inFence := false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(line, "## ") {
			sections = append(sections, section{heading: strings.TrimSpace(strings.TrimPrefix(line, "## "))})
			continue
		}
		if len(sections) > 0 {
			sections[len(sections)-1].body = append(sections[len(sections)-1].body, line)
		}
	}
	return sections
}

// driveFence returns the content of the first ```drive block in the section body.
func driveFence(body []string) (string, bool) {
	var lines []string
	in := false
	for _, line := range body {
		switch {
		case !in && strings.TrimSpace(line) == "```drive":
			in = true
		case in && strings.TrimSpace(line) == "```":
			return strings.Join(lines, "\n") + "\n", true
		case in:
			lines = append(lines, line)
		}
	}
	return "", false
}

// parseDriveBlock reads the key: value lines of a drive block. Every key is required
// once, and no other key is allowed.
func parseDriveBlock(harness, block string) (Drive, error) {
	d := Drive{Harness: harness}
	seen := map[string]bool{}
	for _, line := range strings.Split(block, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return Drive{}, fmt.Errorf("drive block line %q is not key: value", line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if seen[key] {
			return Drive{}, fmt.Errorf("drive block key %q repeated", key)
		}
		seen[key] = true
		switch key {
		case "path":
			d.Path = value
		case "status":
			n, err := strconv.Atoi(value)
			if err != nil {
				return Drive{}, fmt.Errorf("drive block status %q is not a number", value)
			}
			d.Status = n
		case "content-type":
			d.ContentType = value
		case "body-contains":
			d.BodyContains = value
		default:
			return Drive{}, fmt.Errorf("drive block key %q is unknown", key)
		}
	}
	for _, key := range []string{"path", "status", "content-type", "body-contains"} {
		if !seen[key] {
			return Drive{}, fmt.Errorf("drive block lacks %q", key)
		}
	}
	if !strings.HasPrefix(d.Path, "/") {
		return Drive{}, fmt.Errorf("drive block path %q does not start with /", d.Path)
	}
	return d, nil
}

// ParseFeature reads a feature file and its Drive.
func ParseFeature(path string) (Feature, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Feature{}, err
	}
	f := Feature{ID: strings.TrimSuffix(filepath.Base(path), ".md")}
	for _, s := range splitSections(string(raw)) {
		harness, ok := strings.CutPrefix(s.heading, drivingPrefix)
		if !ok {
			continue
		}
		if !knownDrives[harness] {
			return Feature{}, fmt.Errorf("unknown drive id %q", harness)
		}
		block, ok := driveFence(s.body)
		if !ok {
			return Feature{}, fmt.Errorf("section %q has no ```drive block", s.heading)
		}
		d, err := parseDriveBlock(harness, block)
		if err != nil {
			return Feature{}, err
		}
		f.Drive = d
		return f, nil
	}
	return Feature{}, fmt.Errorf("no %q section", drivingPrefix+"<drive id>")
}
