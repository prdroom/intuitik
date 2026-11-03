package main

import "errors"

// Red commit: signature only, it reports failure.
func workflowStep(path, job, step string) (string, map[string]string, error) {
	return "", nil, errors.New("red commit")
}
