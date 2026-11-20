package hk2judge

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/prdroom/intuitik/internal/graph"
)

// SelectPods is the expected Pods of Deployment name from the kubectl lists of one
// namespace (JSON of `kubectl get deployments|replicasets|pods -o json`): the Deployment
// of that name and its UID, and every Pod that is not terminating and whose controller
// ownerReference is a ReplicaSet whose controller ownerReference is that Deployment UID
// (3.2, 6.4). Every other Pod is left out and named in excluded with the reason. An error
// is a list that does not decode or a Deployment name that is not there exactly once.
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
	owned := map[string]bool{} // the ReplicaSet uids whose controller is dep
	for _, rs := range rss.Items {
		if r, ok := rs.controller(); ok && r.APIVersion == "apps/v1" && r.Kind == "Deployment" && r.UID == dep.UID {
			owned[rs.Metadata.UID] = true
		}
	}
	var out []Pod
	var excluded []string
	for _, p := range ps.Items {
		r, ok := p.controller()
		switch {
		case p.Metadata.DeletionTimestamp != nil:
			excluded = append(excluded, "pod "+p.Metadata.Name+": terminating")
		case !ok:
			excluded = append(excluded, "pod "+p.Metadata.Name+": no controller owner")
		case r.APIVersion != "apps/v1" || r.Kind != "ReplicaSet" || !owned[r.UID]:
			excluded = append(excluded, fmt.Sprintf("pod %s: controller %s/%s uid=%s is not a ReplicaSet of Deployment %s uid=%s", p.Metadata.Name, r.Kind, r.Name, r.UID, dep.Name, dep.UID))
		default:
			out = append(out, Pod{UID: p.Metadata.UID, Name: p.Metadata.Name})
		}
	}
	return dep, out, excluded, nil
}

// kubeList is the part of a kubectl list SelectPods reads.
type kubeList struct {
	Items []kubeObject `json:"items"`
}

type kubeObject struct {
	Metadata struct {
		Name              string     `json:"name"`
		UID               string     `json:"uid"`
		DeletionTimestamp *string    `json:"deletionTimestamp"`
		OwnerReferences   []ownerRef `json:"ownerReferences"`
	} `json:"metadata"`
}

type ownerRef struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
	Controller *bool  `json:"controller"`
}

// controller is the ownerReference with controller true, if there is one.
func (o kubeObject) controller() (ownerRef, bool) {
	for _, r := range o.Metadata.OwnerReferences {
		if r.Controller != nil && *r.Controller {
			return r, true
		}
	}
	return ownerRef{}, false
}

// checkOwner prints the owner line of one expected Pod node (3.2): in the answer its
// parent is a ReplicaSet whose parent is the stage's Deployment, by name and UID.
func checkOwner(line func(name, expected, actual string, pass bool), x index, dep Deployment, n graph.Node) {
	want := "ReplicaSet/<any> -> Deployment/" + dep.Name + " uid=" + dep.UID
	actual, pass := "no parent", false
	if n.Parent != nil {
		rs, ok := x.byUID[*n.Parent]
		switch {
		case !ok:
			actual = "parent uid=" + *n.Parent + " not in the answer"
		case rs.APIVersion != "apps/v1" || rs.Kind != "ReplicaSet":
			actual = rs.Kind + "/" + rs.Name
		case rs.Parent == nil:
			actual = "ReplicaSet/" + rs.Name + " -> no parent"
		default:
			d, found := x.byUID[*rs.Parent]
			if !found {
				actual = "ReplicaSet/" + rs.Name + " -> parent uid=" + *rs.Parent + " not in the answer"
				break
			}
			actual = "ReplicaSet/" + rs.Name + " -> " + d.Kind + "/" + d.Name + " uid=" + d.UID
			pass = dep.UID != "" && d.APIVersion == "apps/v1" && d.Kind == "Deployment" && d.Name == dep.Name && d.UID == dep.UID
		}
	}
	line("owner pod="+n.Name, want, actual, pass)
}

// baselinePods prints the expected-Pod and owner lines of every scenario namespace of the
// baseline (3.2: a stage's answer holds no Pod of an earlier rollout) and returns whether
// all of them hold.
func baselinePods(w io.Writer, base baseAnswers) bool {
	ok := true
	for _, id := range IDs {
		ns := ScenarioNamespaces[id]
		line := func(name, expected, actual string, pass bool) {
			fmt.Fprintf(w, "HK2 stage=%s A-0 check=%s namespace=%s expected=%s actual=%s result=%s\n", StageBaseline, name, ns, printable(expected), printable(actual), passFail(pass))
			ok = ok && pass
		}
		e, found := base.expected[ns]
		a, answered := base.namespaces[ns]
		if !found || !answered {
			line("pods", "expected pods and an answer", fmt.Sprintf("expected=%v answer=%v", found, answered), false)
			continue
		}
		checkPods(line, newIndex(a), e.Deployment, e.Pods)
	}
	return ok
}
