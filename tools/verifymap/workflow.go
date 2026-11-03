package main

import (
	"fmt"
	"os"
	"strings"
)

// workflowStep reads one step of a job in a GitHub Actions workflow file written in
// the layout of .github/workflows/ci.yml: jobs at two spaces, `- name:` at six,
// step keys at eight, `env:` entries at ten and the `run: |` script at ten. It
// returns the script without its indentation and the step's env entries.
func workflowStep(path, job, step string) (string, map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")

	start := -1
	for i, l := range lines {
		if l == "  "+job+":" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return "", nil, fmt.Errorf("%s: job %q not found", path, job)
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		l := lines[i]
		if strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "   ") && strings.TrimSpace(l) != "" && !strings.HasPrefix(strings.TrimSpace(l), "#") {
			end = i
			break
		}
	}

	stepStart := -1
	for i := start; i < end; i++ {
		if lines[i] == "      - name: "+step {
			stepStart = i + 1
			break
		}
	}
	if stepStart < 0 {
		return "", nil, fmt.Errorf("%s: job %q has no step %q", path, job, step)
	}
	stepEnd := end
	for i := stepStart; i < end; i++ {
		if strings.HasPrefix(lines[i], "      - ") {
			stepEnd = i
			break
		}
	}

	env := map[string]string{}
	var script []string
	found := false
	for i := stepStart; i < stepEnd; i++ {
		switch lines[i] {
		case "        env:":
			for i+1 < stepEnd && strings.HasPrefix(lines[i+1], "          ") {
				i++
				k, v, ok := strings.Cut(strings.TrimSpace(lines[i]), ":")
				if !ok {
					return "", nil, fmt.Errorf("%s: unreadable env line %q", path, lines[i])
				}
				env[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		case "        run: |":
			found = true
			for i+1 < stepEnd && (strings.HasPrefix(lines[i+1], "          ") || strings.TrimSpace(lines[i+1]) == "") {
				i++
				script = append(script, strings.TrimPrefix(lines[i], "          "))
			}
		}
	}
	if !found {
		return "", nil, fmt.Errorf("%s: step %q of job %q has no `run: |` script", path, step, job)
	}
	text := strings.TrimRight(strings.Join(script, "\n"), "\n") + "\n"
	return text, env, nil
}
