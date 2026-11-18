package graph

import "context"

// Build at R1 of REDSPLIT-T5 (decision 974302a8) reads nothing and answers no node; the
// graph route of R1 does not call it.
func Build(ctx context.Context, open Open, namespace string) (Answer, error) {
	return Answer{Namespace: namespace, Nodes: []Node{}, Refused: []Refusal{}}, nil
}
