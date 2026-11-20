// Package gen is the generator of the synthetic clusters of the server performance
// harness (packet FA2-T12 6.2): the fixed composition of each size as a per-namespace
// plan, the fixture table the judge reads, and the objects to create. Names and counts are
// fixed per size; no random input is used. UIDs and the Pod hash suffixes come from the
// cluster. Every object carries the label intuitik-perf=1.
package gen

import (
	"fmt"
	"strings"

	"github.com/prdroom/intuitik/tools/perfjudge"
)

// Label is the label every synthetic object carries (6.2).
const (
	Label      = "intuitik-perf"
	LabelValue = "1"
)

// NodeAnnotation marks the fake nodes kwok manages (6.2).
const NodeAnnotation = "kwok.x-k8s.io/node"

// StorageClass is kind's default class (WaitForFirstConsumer), so claims stay Pending.
const StorageClass = "standard"

// Image is the container image of every Pod; kwok runs no container, so it is never
// pulled.
const Image = "registry.k8s.io/pause:3.10"

// Deployment is one Deployment of the plan.
type Deployment struct {
	Name     string
	Replicas int
	Node     string
}

// Namespace is the plan of one namespace.
type Namespace struct {
	Name                                          string
	Deployments                                   []Deployment
	Services, Claims, ConfigMaps, Secrets, Events int
}

// Plan is the composition of one size.
type Plan struct {
	Scale      string
	Nodes      []string
	Namespaces []Namespace
	Measured   string // the measured namespace of 6.3: S perf-001, L and XL perf-big
}

// For returns the plan of scale S, L or XL.
func For(scale string) (Plan, error) {
	p := Plan{Scale: scale}
	var names []string
	var nodes, perNsServices, perNsEvents, configMaps, secrets int
	deployments := map[string][]int{} // namespace -> replicas of each Deployment
	switch scale {
	case "S":
		nodes, perNsServices, perNsEvents, configMaps, secrets = 3, 9, 10, 473, 474
		for i := 1; i <= 10; i++ {
			n := fmt.Sprintf("perf-%03d", i)
			names = append(names, n)
			deployments[n] = repeat(3, 10)
		}
		p.Measured = "perf-001"
	case "L", "XL":
		x := 1
		if scale == "XL" {
			x = 4
		}
		nodes, perNsServices, perNsEvents, configMaps, secrets = 100, 30, 20*x, 8033, 8033
		names = append(names, "perf-big")
		deployments["perf-big"] = repeat(12*x, 100)
		for i := 1; i <= 49; i++ {
			n := fmt.Sprintf("perf-%03d", i)
			names = append(names, n)
			if i <= 38 {
				deployments[n] = repeat(10*x, 10)
			}
		}
		p.Measured = "perf-big"
	default:
		return Plan{}, fmt.Errorf("unknown scale %q (S, L or XL)", scale)
	}
	for i := 1; i <= nodes; i++ {
		p.Nodes = append(p.Nodes, fmt.Sprintf("kwok-node-%03d", i))
	}
	k := 0
	for i, n := range names {
		ns := Namespace{Name: n, Services: perNsServices, Claims: perNsServices, Events: perNsEvents,
			ConfigMaps: share(configMaps, len(names), i), Secrets: share(secrets, len(names), i)}
		for j, r := range deployments[n] {
			ns.Deployments = append(ns.Deployments, Deployment{Name: fmt.Sprintf("app-%03d", j+1), Replicas: r, Node: p.Nodes[k%nodes]})
			k++
		}
		p.Namespaces = append(p.Namespaces, ns)
	}
	return p, nil
}

func repeat(n, v int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// share is namespace i's part of total spread evenly over n namespaces, the remainder to
// the first ones.
func share(total, n, i int) int {
	s := total / n
	if i < total%n {
		s++
	}
	return s
}

// Counts is the fixture table: the labelled objects per namespace and kind, one
// ReplicaSet per Deployment and the Deployment's replicas as Pods.
func (p Plan) Counts() []perfjudge.Count {
	out := []perfjudge.Count{{Kind: "Namespace", N: len(p.Namespaces)}, {Kind: "Node", N: len(p.Nodes)}}
	for _, ns := range p.Namespaces {
		pods := 0
		for _, d := range ns.Deployments {
			pods += d.Replicas
		}
		for _, c := range []perfjudge.Count{
			{Kind: "Deployment", N: len(ns.Deployments)}, {Kind: "ReplicaSet", N: len(ns.Deployments)}, {Kind: "Pod", N: pods},
			{Kind: "Service", N: ns.Services}, {Kind: "PersistentVolumeClaim", N: ns.Claims},
			{Kind: "ConfigMap", N: ns.ConfigMaps}, {Kind: "Secret", N: ns.Secrets}, {Kind: "Event", N: ns.Events},
		} {
			c.Namespace = ns.Name
			out = append(out, c)
		}
	}
	return out
}

// Table prints the fixture table, one line per namespace.
func (p Plan) Table() []string {
	out := []string{fmt.Sprintf("PERF fixture scale=%s nodes=%d namespaces=%d measured=%s", p.Scale, len(p.Nodes), len(p.Namespaces), p.Measured)}
	for _, ns := range p.Namespaces {
		var reps []string
		pods := 0
		for _, d := range ns.Deployments {
			pods += d.Replicas
			reps = append(reps, fmt.Sprint(d.Replicas))
		}
		out = append(out, fmt.Sprintf("PERF fixture namespace=%s deployments=%d replicasets=%d pods=%d replicas=[%s] services=%d claims=%d configmaps=%d secrets=%d events=%d",
			ns.Name, len(ns.Deployments), len(ns.Deployments), pods, strings.Join(reps, ","), ns.Services, ns.Claims, ns.ConfigMaps, ns.Secrets, ns.Events))
	}
	return out
}

// Pods is the fixture's Pod count of one namespace.
func (p Plan) Pods(namespace string) int {
	n := 0
	for _, ns := range p.Namespaces {
		if ns.Name == namespace {
			for _, d := range ns.Deployments {
				n += d.Replicas
			}
		}
	}
	return n
}

// Object is one object to create: its collection path and body.
type Object struct {
	Path string
	Body map[string]any
}

func labels(extra map[string]any) map[string]any {
	l := map[string]any{Label: LabelValue}
	for k, v := range extra {
		l[k] = v
	}
	return l
}

func meta(ns, name string, extra map[string]any) map[string]any {
	m := map[string]any{"name": name, "labels": labels(extra)}
	if ns != "" {
		m["namespace"] = ns
	}
	return m
}

// NodeObjects are the fake nodes: annotated for kwok, tainted so that no other Pod lands
// on them unless it tolerates the taint.
func (p Plan) NodeObjects() []Object {
	var out []Object
	for _, n := range p.Nodes {
		res := map[string]any{"cpu": "32", "memory": "256Gi", "pods": "1000"}
		m := meta("", n, map[string]any{"type": "kwok", "kubernetes.io/hostname": n, "kubernetes.io/os": "linux", "kubernetes.io/arch": "amd64"})
		m["annotations"] = map[string]any{NodeAnnotation: "fake", "node.alpha.kubernetes.io/ttl": "0"}
		out = append(out, Object{Path: "/api/v1/nodes", Body: map[string]any{
			"apiVersion": "v1", "kind": "Node", "metadata": m,
			"spec": map[string]any{"taints": []any{map[string]any{"key": NodeAnnotation, "value": "fake", "effect": "NoSchedule"}}},
			"status": map[string]any{"allocatable": res, "capacity": res,
				"nodeInfo": map[string]any{"architecture": "amd64", "operatingSystem": "linux", "kubeletVersion": "fake", "kubeProxyVersion": "fake"}},
		}})
	}
	return out
}

// NamespaceObjects are the namespaces.
func (p Plan) NamespaceObjects() []Object {
	var out []Object
	for _, ns := range p.Namespaces {
		out = append(out, Object{Path: "/api/v1/namespaces", Body: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": meta("", ns.Name, nil)}})
	}
	return out
}

// Objects are the namespaced objects of one namespace: Deployments (each Pod bound to its
// fake node by nodeName, so no scheduling is needed), Services selecting the Deployments
// in turn, claims, ConfigMaps, Secrets with one fixed non-secret value, and Events.
func (ns Namespace) Objects() []Object {
	var out []Object
	core := "/api/v1/namespaces/" + ns.Name + "/"
	for _, d := range ns.Deployments {
		sel := map[string]any{"app": d.Name}
		out = append(out, Object{Path: "/apis/apps/v1/namespaces/" + ns.Name + "/deployments", Body: map[string]any{
			"apiVersion": "apps/v1", "kind": "Deployment", "metadata": meta(ns.Name, d.Name, sel),
			"spec": map[string]any{"replicas": d.Replicas, "selector": map[string]any{"matchLabels": sel},
				"template": map[string]any{"metadata": map[string]any{"labels": labels(sel)},
					"spec": map[string]any{"nodeName": d.Node, "automountServiceAccountToken": false,
						"tolerations": []any{map[string]any{"key": NodeAnnotation, "operator": "Exists", "effect": "NoSchedule"}},
						"containers":  []any{map[string]any{"name": "app", "image": Image}}}}},
		}})
	}
	for i := 1; i <= ns.Services; i++ {
		app := fmt.Sprintf("app-%03d", i)
		if len(ns.Deployments) > 0 {
			app = ns.Deployments[(i-1)%len(ns.Deployments)].Name
		}
		out = append(out, Object{Path: core + "services", Body: map[string]any{
			"apiVersion": "v1", "kind": "Service", "metadata": meta(ns.Name, fmt.Sprintf("svc-%03d", i), nil),
			"spec": map[string]any{"selector": map[string]any{"app": app}, "ports": []any{map[string]any{"port": 80, "targetPort": 8080}}},
		}})
	}
	for i := 1; i <= ns.Claims; i++ {
		out = append(out, Object{Path: core + "persistentvolumeclaims", Body: map[string]any{
			"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": meta(ns.Name, fmt.Sprintf("data-%03d", i), nil),
			"spec": map[string]any{"storageClassName": StorageClass, "accessModes": []any{"ReadWriteOnce"},
				"resources": map[string]any{"requests": map[string]any{"storage": "1Gi"}}},
		}})
	}
	for i := 1; i <= ns.ConfigMaps; i++ {
		out = append(out, Object{Path: core + "configmaps", Body: map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap", "metadata": meta(ns.Name, fmt.Sprintf("config-%03d", i), nil),
			"data": map[string]any{"value": "perf"},
		}})
	}
	for i := 1; i <= ns.Secrets; i++ {
		out = append(out, Object{Path: core + "secrets", Body: map[string]any{
			"apiVersion": "v1", "kind": "Secret", "metadata": meta(ns.Name, fmt.Sprintf("secret-%03d", i), nil),
			"type": "Opaque", "stringData": map[string]any{"value": "perf"},
		}})
	}
	for i := 1; i <= ns.Events; i++ {
		out = append(out, Object{Path: core + "events", Body: map[string]any{
			"apiVersion": "v1", "kind": "Event", "metadata": meta(ns.Name, fmt.Sprintf("perf-event-%03d", i), nil),
			"involvedObject": map[string]any{"apiVersion": "v1", "kind": "Service", "namespace": ns.Name, "name": "svc-001"},
			"reason":         "PerfFixture", "message": "perf fixture event", "type": "Normal", "count": 1,
			"source":         map[string]any{"component": "intuitik-perf"},
			"firstTimestamp": "2026-01-01T00:00:00Z", "lastTimestamp": "2026-01-01T00:00:00Z",
		}})
	}
	return out
}
