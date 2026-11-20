package hk2judge

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/prdroom/intuitik/internal/graph"
)

// Red stub of commit R3 (F1 of review 5987053962): the behaviour of H2. SelectPods takes
// every Pod that is not terminating, whatever its owner; checkOwner prints nothing and
// baselinePods checks nothing.

// SelectPods is the expected Pods of Deployment name from the kubectl lists of one
// namespace. Red stub: every Pod that is not terminating.
func SelectPods(name string, deployments, replicaSets, pods []byte) (Deployment, []Pod, []string, error) {
	var ds, rss, ps kubeList
	for _, l := range []struct {
		what string
		raw  []byte
		v    *kubeList
	}{{"deployments", deployments, &ds}, {"replicasets", replicaSets, &rss}, {"pods", pods, &ps}} {
		if err := json.Unmarshal(l.raw, l.v); err != nil {
			return Deployment{}, nil, nil, fmt.Errorf("%s do not decode: %v", l.what, err)
		}
	}
	var dep Deployment
	found := 0
	for _, d := range ds.Items {
		if d.Metadata.Name == name {
			dep = Deployment{Name: name, UID: d.Metadata.UID}
			found++
		}
	}
	if found != 1 {
		return Deployment{}, nil, nil, fmt.Errorf("Deployment %s found %d times", name, found)
	}
	var out []Pod
	var excluded []string
	for _, p := range ps.Items {
		if p.Metadata.DeletionTimestamp != nil {
			excluded = append(excluded, "pod "+p.Metadata.Name+": terminating")
			continue
		}
		out = append(out, Pod{UID: p.Metadata.UID, Name: p.Metadata.Name})
	}
	return dep, out, excluded, nil
}

// kubeList is the part of a kubectl list SelectPods reads.
type kubeList struct {
	Items []kubeObject `json:"items"`
}

type kubeObject struct {
	Metadata struct {
		Name              string  `json:"name"`
		UID               string  `json:"uid"`
		DeletionTimestamp *string `json:"deletionTimestamp"`
	} `json:"metadata"`
}

// checkOwner prints the owner line of one expected Pod node. Red stub: nothing.
func checkOwner(line func(name, expected, actual string, pass bool), x index, dep Deployment, n graph.Node) {
}

// baselinePods checks the expected Pods of every scenario namespace of the baseline. Red
// stub: nothing is checked.
func baselinePods(w io.Writer, base baseAnswers) bool {
	return true
}
