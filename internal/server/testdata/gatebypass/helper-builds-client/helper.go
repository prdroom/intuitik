package server

import (
	"context"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

// Defect: a helper outside the gate file builds its own readonly client.
func clusterVersion(o readonly.Outcome) error {
	c, err := readonly.NewClient(o)
	if err != nil {
		return err
	}
	_, err = c.Get(context.Background(), "/version")
	return err
}
