package perfjudge

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Resource is one API resource as discovery serves it.
type Resource struct {
	Group, Version, Resource string
	Namespaced               bool
	Verbs                    []string
}

// Key is the group-qualified resource name the lines print: "pods", "replicasets.apps".
func (r Resource) Key() string {
	if r.Group == "" {
		return r.Resource
	}
	return r.Resource + "." + r.Group
}

func key(group, resource string) string { return Resource{Group: group, Resource: resource}.Key() }

func res(group, version, resource string) Resource {
	return Resource{Group: group, Version: version, Resource: resource}
}

// The source tables of the product at 97766bb5 (packet 6.3 C5 expected sets): a call
// lists exactly these resources, intersected with what the cluster serves.
var (
	// clusterKinds, internal/graph/cluster.go 55-59.
	clusterKinds = []Resource{res("", "v1", "namespaces"), res("", "v1", "nodes"), res("storage.k8s.io", "v1", "storageclasses"), res("", "v1", "persistentvolumes")}
	// builtInKinds, engine.go 64-71; bridgeKinds, bridges.go 21-25; configKinds, states.go 15-17.
	namespaceKinds = []Resource{
		res("", "v1", "pods"), res("apps", "v1", "replicasets"), res("apps", "v1", "deployments"), res("apps", "v1", "statefulsets"),
		res("apps", "v1", "daemonsets"), res("batch", "v1", "jobs"), res("batch", "v1", "cronjobs"),
		res("", "v1", "services"), res("", "v1", "persistentvolumeclaims"), res("networking.k8s.io", "v1", "ingresses"),
		res("autoscaling", "v2", "horizontalpodautoscalers"),
		res("", "v1", "configmaps"), res("", "v1", "secrets"),
	}
	// volumeKinds, states.go 22-24: listed when the claims list returned a claim.
	volumeKinds = []Resource{res("", "v1", "persistentvolumes"), res("storage.k8s.io", "v1", "storageclasses")}
	// builtInGroups, engine.go 23-51: groups whose resources are not listed as custom kinds.
	builtInGroups = map[string]bool{"": true, "admissionregistration.k8s.io": true, "apiextensions.k8s.io": true,
		"apiregistration.k8s.io": true, "apps": true, "authentication.k8s.io": true, "authorization.k8s.io": true,
		"autoscaling": true, "batch": true, "certificates.k8s.io": true, "coordination.k8s.io": true,
		"discovery.k8s.io": true, "events.k8s.io": true, "flowcontrol.apiserver.k8s.io": true,
		"internal.apiserver.k8s.io": true, "lifecycle.k8s.io": true, "networking.k8s.io": true, "node.k8s.io": true,
		"policy": true, "rbac.authorization.k8s.io": true, "resource.k8s.io": true, "scheduling.k8s.io": true,
		"storage.k8s.io": true, "storagemigration.k8s.io": true, "metrics.k8s.io": true,
		"custom.metrics.k8s.io": true, "external.metrics.k8s.io": true}
)

// ExpectedSet is the C5 set of one call (packet 6.3): for the cluster call the cluster
// kinds; for a namespace call the 13 namespace kinds, the volume kinds when the namespace
// holds claims, and the namespaced list-able resources of groups outside the built-in
// groups. Each entry is kept only when discovery serves its group, version and resource.
func ExpectedSet(cluster bool, served []Resource, holdsClaims bool) []Resource {
	serves := map[string]bool{}
	for _, s := range served {
		serves[s.Group+"/"+s.Version+"/"+s.Resource] = true
	}
	var src []Resource
	if cluster {
		src = clusterKinds
	} else {
		src = append(append(src, namespaceKinds...), volumeKinds...)
		if !holdsClaims {
			src = namespaceKinds
		}
	}
	var out []Resource
	seen := map[string]bool{}
	for _, r := range src {
		if serves[r.Group+"/"+r.Version+"/"+r.Resource] && !seen[r.Key()] {
			seen[r.Key()] = true
			out = append(out, r)
		}
	}
	if cluster {
		return out
	}
	var custom []Resource
	for _, s := range served {
		if builtInGroups[s.Group] || !s.Namespaced || strings.Contains(s.Resource, "/") || !hasVerb(s.Verbs, "list") || seen[s.Key()] {
			continue
		}
		seen[s.Key()] = true
		custom = append(custom, s)
	}
	sort.Slice(custom, func(i, j int) bool { return custom[i].Key() < custom[j].Key() })
	return append(out, custom...)
}

func hasVerb(verbs []string, v string) bool {
	for _, x := range verbs {
		if x == v {
			return true
		}
	}
	return false
}

// SetResult is the C5 result of one call.
type SetResult struct {
	Call    string
	User    string
	Matched int // requests of user inside the call window (list and get on a resource)
	K       int // size of the expected set
	Missing []string
	Pass    bool
}

// Line prints the result.
func (s SetResult) Line() string {
	missing := "none"
	if len(s.Missing) > 0 {
		missing = strings.Join(s.Missing, ",")
	}
	return fmt.Sprintf("PERF C5 call=%s user=%s matched=%d K=%d missing=%s result=%s", s.Call, s.User, s.Matched, s.K, missing, passFail(s.Pass))
}

// JudgeIdentity is the identity filter of C5: the requests of user inside the call window
// must be at least K (the expected set's size), and every expected resource must have at
// least one list. An empty log or another user gives 0 matches and FAIL, never 0 as PASS.
func JudgeIdentity(call string, reqs []Request, user string, span Span, expected []Resource) SetResult {
	// Red stub of packet FA2-T12 6.5: PASS with zero counts; the sampler returns 0.
	return SetResult{Call: call, User: user, Pass: true}
}

// PageLine is the N-6 read pattern of one resource in one call (packet 3.3).
type PageLine struct {
	Call      string
	Resource  string
	Lists     int
	Limits    []string // distinct limit values of the lists, "none" when absent
	Continue  int
	Gets      int
	Pass      bool
	Shortfall []string
}

// Line prints the result with the T11 marker (P-T12-2): the marker does not change it.
func (p PageLine) Line() string {
	limits := "none"
	if len(p.Limits) > 0 {
		limits = strings.Join(p.Limits, ",")
	}
	s := fmt.Sprintf("PERF N-6 call=%s resource=%s list=%d limit=%s continue=%d get=%d result=%s handled-in=T11", p.Call, p.Resource, p.Lists, limits, p.Continue, p.Gets, passFail(p.Pass))
	if len(p.Shortfall) > 0 {
		s += " shortfall=" + strings.Join(p.Shortfall, ";")
	}
	return s
}

// PageLimit is the page size of PRD line 209.
const PageLimit = "500"

// JudgePages judges the read pattern of one call (packet 3.3, C4): for each resource of the
// expected set and any other resource the call read, PASS only when the call listed it and
// every list carried limit=500. For the Pods of the measured namespace (measured true) the
// lists must also be exactly ceil(podsWant/500) pages, all but the first with continue, and
// the answer must hold podsWant Pod nodes (podNodes, counted from the answer body).
func JudgePages(call string, reqs []Request, user string, span Span, expected []Resource, measured bool, podsWant, podNodes int) []PageLine {
	// Red stub of packet FA2-T12 6.5: PASS with zero counts; the sampler returns 0.
	var out []PageLine
	for _, e := range expected {
		out = append(out, PageLine{Call: call, Resource: e.Key(), Pass: true})
	}
	return out
}

// CountPodNodes counts the core Pod nodes of one graph answer body.
func CountPodNodes(body []byte) (int, error) {
	var a struct {
		Nodes []struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(body, &a); err != nil {
		return 0, err
	}
	n := 0
	for _, x := range a.Nodes {
		if x.APIVersion == "v1" && x.Kind == "Pod" {
			n++
		}
	}
	return n, nil
}

func passFail(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}
