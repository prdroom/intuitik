package graph

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

// gatewayGroup is the API group of the Gateway API (Q-P6: HTTPRoute -> Service only).
const gatewayGroup = "gateway.networking.k8s.io"

// bridgeKinds are the four kinds that a call lists after the owner chains, in the order
// of FA2-T6 E.3.
var bridgeKinds = []listedKind{
	{readonly.Resource{Version: "v1", Resource: "services"}, "Service"},
	{readonly.Resource{Version: "v1", Resource: "persistentvolumeclaims"}, "PersistentVolumeClaim"},
	{readonly.Resource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}, "Ingress"},
	{readonly.Resource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}, "HorizontalPodAutoscaler"},
}

// This file is the red stub of packet FA2-T6 section 5 (items s1 to s9, s4 as decided by
// T6-S4-ENV): one naive extractor, wrong in a different way for each criterion. The green
// commit replaces it.

// listBridgeKinds sends the four LISTs (s1); their objects become nodes nested by their
// ownerReferences like the FA2-T5 children. A refused HTTPRoutes LIST gets a second
// refused entry (s9).
func (c *call) listBridgeKinds(ctx context.Context, refused *[]Refusal) error {
	var added []*entry
	for _, k := range bridgeKinds {
		list, err := c.list(ctx, k.res)
		if err != nil {
			if code, ok := statusCode(err); ok && code == http.StatusForbidden {
				*refused = append(*refused, Refusal{Kind: k.kind, Status: code, Reason: strconv.Itoa(code), Text: KindText(k.kind, code)})
				continue
			}
			return c.fail(err)
		}
		for i := range list.Items {
			added = append(added, c.add(&list.Items[i], k))
		}
	}
	for _, e := range added {
		if err := c.place(ctx, e, nil); err != nil {
			return err
		}
	}
	if c.routeRefused {
		*refused = append(*refused, Refusal{Kind: "HTTPRoute", Status: http.StatusForbidden, Reason: "403", Text: KindText("HTTPRoute", http.StatusForbidden)})
	}
	return nil
}

// fragment is the JSON of one part of an object (s2).
func fragment(v any) []string {
	b, _ := json.Marshal(v)
	return []string{string(b)}
}

func field(m map[string]any, path ...string) any {
	var v any = m
	for _, p := range path {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[p]
	}
	return v
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func items(v any) []map[string]any {
	l, _ := v.([]any)
	var out []map[string]any
	for _, x := range l {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// bridges makes the bridges of the stub: settings are the JSON of the spec fragment (s2),
// targets are joined by name in any namespace and group (s3), Service targets that are
// not nodes give no bridge (s5), HTTPRoutes are read as v1 whatever their version (s6),
// bridges keep the order in which their LISTs answered (s7), and with no Gateway API
// group one unsupported entry of version "none" is added (s8). After the answer is built
// it GETs each ConfigMap and Secret that an env or envFrom names (s4).
func (c *call) bridges(ctx context.Context, a *Answer) error {
	byName := map[string]string{}
	services := map[string]bool{}
	for _, n := range a.Nodes {
		if _, ok := byName[n.Name]; !ok {
			byName[n.Name] = n.UID
		}
		if n.Kind == "Service" {
			services[n.Name] = true
		}
	}
	to := func(kind, name string) Target {
		t := Target{Namespace: c.ns, Kind: kind, Name: name}
		if uid, ok := byName[name]; ok {
			t.UID = &uid
		}
		return t
	}
	add := func(kind string, e *entry, t Target, settings []string) {
		a.Bridges = append(a.Bridges, Bridge{Kind: kind, From: From{UID: e.uid}, To: t, Settings: settings})
	}
	var pods []*entry
	for _, e := range c.arrival {
		if e.kind == "Pod" && e.obj != nil {
			pods = append(pods, e)
		}
	}
	type ref struct{ kind, name string }
	var envRefs []ref
	for _, e := range c.arrival {
		if e.obj == nil {
			continue
		}
		o := e.obj.Object
		switch e.kind {
		case "Ingress":
			for _, r := range items(field(o, "spec", "rules")) {
				for _, p := range items(field(r, "http", "paths")) {
					if name := str(field(p, "backend", "service", "name")); name != "" && services[name] {
						add(BridgeIngressService, e, to("Service", name), fragment(p))
					}
				}
			}
			if name := str(field(o, "spec", "defaultBackend", "service", "name")); name != "" && services[name] {
				add(BridgeIngressService, e, to("Service", name), fragment(field(o, "spec", "defaultBackend")))
			}
		case "Service":
			sel, _ := field(o, "spec", "selector").(map[string]any)
			if len(sel) == 0 || str(field(o, "spec", "type")) == "ExternalName" {
				continue
			}
			for _, p := range pods {
				labels := p.obj.GetLabels()
				match := true
				for k, v := range sel {
					match = match && labels[k] == str(v)
				}
				if match {
					add(BridgeServicePod, e, to("Pod", p.name), fragment(field(o, "spec")))
				}
			}
		case "Pod":
			containers := append(items(field(o, "spec", "containers")), items(field(o, "spec", "initContainers"))...)
			env := func() []string {
				if len(containers) == 0 {
					return fragment(nil)
				}
				return fragment(containers[0]["env"])
			}
			seen := map[ref]bool{}
			target := func(kind, name string, settings []string) {
				if name == "" || seen[ref{kind, name}] {
					return
				}
				seen[ref{kind, name}] = true
				bk := BridgePodConfigMap
				if kind == "Secret" {
					bk = BridgePodSecret
				}
				add(bk, e, to(kind, name), settings)
			}
			for _, ct := range containers {
				for _, ev := range items(ct["env"]) {
					if n := str(field(ev, "valueFrom", "configMapKeyRef", "name")); n != "" {
						envRefs = append(envRefs, ref{"ConfigMap", n})
						target("ConfigMap", n, fragment(ct["env"]))
					}
					if n := str(field(ev, "valueFrom", "secretKeyRef", "name")); n != "" {
						envRefs = append(envRefs, ref{"Secret", n})
						target("Secret", n, fragment(ct["env"]))
					}
				}
				for _, ef := range items(ct["envFrom"]) {
					if n := str(field(ef, "configMapRef", "name")); n != "" {
						envRefs = append(envRefs, ref{"ConfigMap", n})
						target("ConfigMap", n, fragment(ct["env"]))
					}
					if n := str(field(ef, "secretRef", "name")); n != "" {
						envRefs = append(envRefs, ref{"Secret", n})
						target("Secret", n, fragment(ct["env"]))
					}
				}
			}
			for _, v := range items(field(o, "spec", "volumes")) {
				if n := str(field(v, "persistentVolumeClaim", "claimName")); n != "" {
					add(BridgePodPVC, e, to("PersistentVolumeClaim", n), fragment(v))
				}
				target("ConfigMap", str(field(v, "configMap", "name")), env())
				target("Secret", str(field(v, "secret", "secretName")), env())
				for _, s := range items(field(v, "projected", "sources")) {
					target("ConfigMap", str(field(s, "configMap", "name")), env())
					target("Secret", str(field(s, "secret", "name")), env())
				}
			}
		case "PersistentVolumeClaim":
			if n := str(field(o, "spec", "volumeName")); n != "" {
				add(BridgePVCPV, e, to("PersistentVolume", n), fragment(field(o, "spec")))
			}
		case "HorizontalPodAutoscaler":
			if n := str(field(o, "spec", "scaleTargetRef", "name")); n != "" {
				add(BridgeHPATarget, e, to(str(field(o, "spec", "scaleTargetRef", "kind")), n), fragment(field(o, "spec", "scaleTargetRef")))
			}
		case "HTTPRoute":
			for _, r := range items(field(o, "spec", "rules")) {
				for _, b := range items(r["backendRefs"]) {
					if n := str(b["name"]); n != "" && services[n] {
						add(BridgeHTTPRouteService, e, to("Service", n), fragment(r))
					}
				}
			}
		}
	}
	if !c.gateway {
		a.Unsupported = append(a.Unsupported, Unsupported{Group: gatewayGroup, Kind: "HTTPRoute", Version: "none", Text: "Unsupported version none"})
	}
	done := map[ref]bool{}
	for _, r := range envRefs {
		if done[r] {
			continue
		}
		done[r] = true
		res := readonly.Resource{Version: "v1", Resource: "configmaps"}
		if r.kind == "Secret" {
			res.Resource = "secrets"
		}
		if rd, err := c.reader(ctx); err == nil {
			rd.Object(ctx, res, c.ns, r.name)
		}
	}
	return nil
}
