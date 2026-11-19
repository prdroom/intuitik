package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

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

// routeVersions are the HTTPRoute versions whose fields the bridges are read from; the
// fields read have the same schema in both (B4). No other version is converted.
var routeVersions = map[string]bool{"v1": true, "v1beta1": true}

// listBridgeKinds sends the four LISTs of E.3. Their objects are nodes directly under the
// namespace with owner state none, whatever their ownerReferences (B7). A 403 adds the
// GA-4 entry and the call goes on; any other failure fails the whole call and names the
// kind (B3), except a stopped call and the end of the call's context, which keep the
// FA2-T5 answers.
func (c *call) listBridgeKinds(ctx context.Context, refused *[]Refusal) error {
	for _, k := range bridgeKinds {
		list, err := c.list(ctx, k.res)
		if err != nil {
			if code, ok := statusCode(err); ok && code == http.StatusForbidden {
				*refused = append(*refused, Refusal{Kind: k.kind, Status: code, Reason: strconv.Itoa(code), Text: KindText(k.kind, code)})
				if k.kind == "Service" {
					c.servicesRefused = true
				}
				continue
			}
			var le *LoadError
			if errors.Is(err, ErrStopped) || errors.As(err, &le) || ctx.Err() != nil {
				return c.fail(err)
			}
			return &LoadError{Namespace: c.ns, Reason: reasonOf(err), Kind: k.kind}
		}
		for i := range list.Items {
			e := c.add(&list.Items[i], k)
			e.state, e.parent, e.ref, e.text, e.placed = StateNone, "", "", "", true
		}
	}
	return nil
}

// targetKey identifies an object by group, kind, namespace and name (B5).
type targetKey struct{ group, kind, namespace, name string }

// bridgeSet collects the bridges of one call, one per (kind, from, target), with their
// settings in the order they are found.
type bridgeSet struct {
	c     *call
	nodes map[targetKey]string // the uid of each node
	index map[string]int       // "<kind> <from uid> <target>" -> position in list
	list  []Bridge
}

// add appends settings to the bridge of kind from e to t, making it on first use.
func (s *bridgeSet) add(kind string, e *entry, t targetKey, settings ...string) *Bridge {
	key := fmt.Sprintf("%s %s %s/%s/%s/%s", kind, e.uid, t.group, t.kind, t.namespace, t.name)
	i, ok := s.index[key]
	if !ok {
		to := Target{Group: t.group, Namespace: t.namespace, Kind: t.kind, Name: t.name}
		if uid, found := s.nodes[t]; found {
			to.UID = &uid
		}
		s.list = append(s.list, Bridge{Kind: kind, From: From{UID: e.uid}, To: to, Settings: []string{}})
		i = len(s.list) - 1
		s.index[key] = i
	}
	s.list[i].Settings = append(s.list[i].Settings, settings...)
	return &s.list[i]
}

// bridges makes the bridges of the answer from the objects already read, without any
// further request (E.3 step (6), E.4), and the unsupported entry of an HTTPRoute resource
// read at a version that is not named (B4). Bridges are in the order of B6.
func (c *call) bridges(ctx context.Context, a *Answer) error {
	s := &bridgeSet{c: c, nodes: map[targetKey]string{}, index: map[string]int{}}
	for _, e := range c.entries {
		g, _ := splitAPIVersion(e.apiVersion)
		s.nodes[targetKey{g, e.kind, c.ns, e.name}] = e.uid
	}
	entries := c.sorted()
	for _, e := range entries {
		if e.obj == nil {
			continue
		}
		g, v := splitAPIVersion(e.apiVersion)
		switch {
		case g == "" && e.kind == "Pod":
			s.pod(e)
		case g == "" && e.kind == "Service":
			s.service(e, entries)
		case g == "" && e.kind == "PersistentVolumeClaim":
			s.claim(e)
		case g == "networking.k8s.io" && e.kind == "Ingress":
			s.ingress(e)
		case g == "autoscaling" && e.kind == "HorizontalPodAutoscaler":
			s.hpa(e)
		case g == gatewayGroup && e.kind == "HTTPRoute" && routeVersions[v]:
			s.route(e)
		}
	}
	if c.route != nil && !routeVersions[c.route.res.Version] {
		a.Unsupported = append(a.Unsupported, Unsupported{Group: gatewayGroup, Kind: "HTTPRoute", Version: c.route.res.Version, Text: UnsupportedText(c.route.res.Version)})
	}
	key := func(b Bridge) []string {
		from := c.entries[b.From.UID]
		return []string{from.kind, from.name, b.Kind, b.To.Kind, b.To.Name, strings.Join(b.Settings, "\x00"), b.To.Group, b.To.Namespace, b.From.UID}
	}
	sort.Slice(s.list, func(i, j int) bool {
		ki, kj := key(s.list[i]), key(s.list[j])
		for n := range ki {
			if ki[n] != kj[n] {
				return ki[n] < kj[n]
			}
		}
		return false
	})
	a.Bridges = append(a.Bridges, s.list...)
	return nil
}

// field decodes the top-level field name of e's object (spec, status) into v. A Pod
// container's env values are never decoded: the types below have no field for them (B2).
func field(e *entry, name string, v any) {
	raw, err := json.Marshal(e.obj.Object[name])
	if err == nil {
		json.Unmarshal(raw, v)
	}
}

// The references of a Pod spec to a ConfigMap or Secret. Optional is the reference's
// optional field, unset is false (E.4: a flag kept for T7a, never text).
type nameRef struct {
	Name     string `json:"name"`
	Optional bool   `json:"optional"`
}

type keyRef struct {
	Name     string `json:"name"`
	Key      string `json:"key"`
	Optional bool   `json:"optional"`
}

type keyItem struct {
	Key string `json:"key"`
}

type keySource struct {
	Name     string    `json:"name"`
	Items    []keyItem `json:"items"`
	Optional bool      `json:"optional"`
}

type podContainer struct {
	Name string `json:"name"`
	Env  []struct {
		Name      string `json:"name"`
		ValueFrom *struct {
			ConfigMapKeyRef *keyRef `json:"configMapKeyRef"`
			SecretKeyRef    *keyRef `json:"secretKeyRef"`
		} `json:"valueFrom"`
	} `json:"env"`
	EnvFrom []struct {
		Prefix       string   `json:"prefix"`
		ConfigMapRef *nameRef `json:"configMapRef"`
		SecretRef    *nameRef `json:"secretRef"`
	} `json:"envFrom"`
	VolumeMounts []struct {
		Name      string `json:"name"`
		MountPath string `json:"mountPath"`
		ReadOnly  bool   `json:"readOnly"`
	} `json:"volumeMounts"`
}

type podVolume struct {
	Name                  string `json:"name"`
	PersistentVolumeClaim *struct {
		ClaimName string `json:"claimName"`
	} `json:"persistentVolumeClaim"`
	Ephemeral *json.RawMessage `json:"ephemeral"`
	ConfigMap *keySource       `json:"configMap"`
	Secret    *struct {
		SecretName string    `json:"secretName"`
		Items      []keyItem `json:"items"`
		Optional   bool      `json:"optional"`
	} `json:"secret"`
	Projected *struct {
		Sources []struct {
			ConfigMap *keySource `json:"configMap"`
			Secret    *keySource `json:"secret"`
		} `json:"sources"`
	} `json:"projected"`
}

type podSpec struct {
	Volumes        []podVolume    `json:"volumes"`
	Containers     []podContainer `json:"containers"`
	InitContainers []podContainer `json:"initContainers"`
}

// mount is one mount of a volume by one container.
type mount struct {
	container, path string
	readOnly        bool
}

// pod makes the Pod -> PVC, Pod -> ConfigMap and Pod -> Secret bridges of one Pod
// (section 3.1): references in containers then initContainers, in spec order; env and
// envFrom before volumes. Each settings entry of a Pod -> ConfigMap or Pod -> Secret
// bridge carries the optional flag of its reference. No ConfigMap or Secret is read (B1).
func (s *bridgeSet) pod(e *entry) {
	var spec podSpec
	field(e, "spec", &spec)
	containers := append(append([]podContainer{}, spec.Containers...), spec.InitContainers...)
	ref := func(kind, name string, optional bool, setting string) {
		if name == "" {
			return
		}
		bk := BridgePodConfigMap
		if kind == "Secret" {
			bk = BridgePodSecret
		}
		b := s.add(bk, e, targetKey{"", kind, s.c.ns, name}, setting)
		b.Optional = append(b.Optional, optional)
	}
	for _, ct := range containers {
		for _, ev := range ct.Env {
			if ev.ValueFrom == nil {
				continue
			}
			if r := ev.ValueFrom.ConfigMapKeyRef; r != nil {
				ref("ConfigMap", r.Name, r.Optional, EnvSetting(ct.Name, ev.Name, r.Key))
			}
			if r := ev.ValueFrom.SecretKeyRef; r != nil {
				ref("Secret", r.Name, r.Optional, EnvSetting(ct.Name, ev.Name, r.Key))
			}
		}
		for _, ef := range ct.EnvFrom {
			if r := ef.ConfigMapRef; r != nil {
				ref("ConfigMap", r.Name, r.Optional, EnvFromSetting(ct.Name, ef.Prefix))
			}
			if r := ef.SecretRef; r != nil {
				ref("Secret", r.Name, r.Optional, EnvFromSetting(ct.Name, ef.Prefix))
			}
		}
	}
	mounts := func(volume string) []mount {
		var out []mount
		for _, ct := range containers {
			for _, m := range ct.VolumeMounts {
				if m.Name == volume {
					out = append(out, mount{ct.Name, m.MountPath, m.ReadOnly})
				}
			}
		}
		return out
	}
	keyed := func(kind, name string, optional bool, volume string, items []keyItem) {
		if name == "" {
			return
		}
		var keys []string
		for _, it := range items {
			keys = append(keys, it.Key)
		}
		ms := mounts(volume)
		if len(ms) == 0 {
			ref(kind, name, optional, UnmountedSetting(volume))
		}
		for _, m := range ms {
			ref(kind, name, optional, VolumeSetting(m.container, m.path, keys))
		}
	}
	var claims []string
	claimMounts := map[string][]mount{}
	for _, v := range spec.Volumes {
		claim := ""
		switch {
		case v.PersistentVolumeClaim != nil:
			claim = v.PersistentVolumeClaim.ClaimName
		case v.Ephemeral != nil:
			claim = e.name + "-" + v.Name
		}
		if claim != "" {
			if _, seen := claimMounts[claim]; !seen {
				claims = append(claims, claim)
				claimMounts[claim] = []mount{}
			}
			claimMounts[claim] = append(claimMounts[claim], mounts(v.Name)...)
		}
		if c := v.ConfigMap; c != nil {
			keyed("ConfigMap", c.Name, c.Optional, v.Name, c.Items)
		}
		if c := v.Secret; c != nil {
			keyed("Secret", c.SecretName, c.Optional, v.Name, c.Items)
		}
		if v.Projected != nil {
			for _, src := range v.Projected.Sources {
				if c := src.ConfigMap; c != nil {
					keyed("ConfigMap", c.Name, c.Optional, v.Name, c.Items)
				}
				if c := src.Secret; c != nil {
					keyed("Secret", c.Name, c.Optional, v.Name, c.Items)
				}
			}
		}
	}
	for _, claim := range claims {
		t := targetKey{"", "PersistentVolumeClaim", s.c.ns, claim}
		b := s.add(BridgePodPVC, e, t, ClaimSetting(claim))
		for _, m := range claimMounts[claim] {
			b.Settings = append(b.Settings, MountSetting(m.container, m.path, m.readOnly))
		}
		if uid, ok := s.nodes[t]; ok && s.c.entries[uid].obj != nil {
			var cs struct {
				AccessModes []string `json:"accessModes"`
			}
			field(s.c.entries[uid], "spec", &cs)
			if len(cs.AccessModes) > 0 {
				b.Settings = append(b.Settings, AccessSetting(cs.AccessModes))
			}
		}
		// Red stub R3 (packet FA2-T6 section 5 s2, review 5969268537 F4): the settings of a
		// Pod -> PVC bridge are the JSON of the spec fragment it came from. H4 removes it.
		b.Settings = []string{redFragment(spec.Volumes)}
	}
}

// redFragment is the JSON of a spec fragment (red stub R3 only).
func redFragment(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// service makes one Service -> Pod bridge per Pod of the answer whose labels hold every
// pair of the Service's selector; an ExternalName Service and one without selector give
// none (E.4).
func (s *bridgeSet) service(e *entry, entries []*entry) {
	var spec struct {
		Type     string            `json:"type"`
		Selector map[string]string `json:"selector"`
		Ports    []struct {
			Port       int             `json:"port"`
			TargetPort json.RawMessage `json:"targetPort"`
			Protocol   string          `json:"protocol"`
		} `json:"ports"`
	}
	field(e, "spec", &spec)
	if len(spec.Selector) == 0 || spec.Type == "ExternalName" {
		return
	}
	settings := []string{SelectorSetting(spec.Selector)}
	for _, p := range spec.Ports {
		target := strconv.Itoa(p.Port)
		var name string
		var number int
		switch {
		case json.Unmarshal(p.TargetPort, &name) == nil && name != "":
			target = name
		case json.Unmarshal(p.TargetPort, &number) == nil && number != 0:
			target = strconv.Itoa(number)
		}
		settings = append(settings, PortSetting(p.Port, target, p.Protocol))
	}
	for _, p := range entries {
		if g, _ := splitAPIVersion(p.apiVersion); g != "" || p.kind != "Pod" || p.obj == nil {
			continue
		}
		labels := p.obj.GetLabels()
		match := true
		for k, v := range spec.Selector {
			if l, ok := labels[k]; !ok || l != v {
				match = false
			}
		}
		if match {
			s.add(BridgeServicePod, e, targetKey{"", "Pod", s.c.ns, p.name}, settings...)
		}
	}
}

// claim makes the PVC -> PV bridge of a bound claim; the PV is not read (6.3).
func (s *bridgeSet) claim(e *entry) {
	var spec struct {
		VolumeName       string  `json:"volumeName"`
		StorageClassName *string `json:"storageClassName"`
	}
	var status struct {
		Capacity map[string]string `json:"capacity"`
	}
	field(e, "spec", &spec)
	field(e, "status", &status)
	if spec.VolumeName == "" {
		return
	}
	settings := []string{ClassSetting(spec.StorageClassName)}
	if c := status.Capacity["storage"]; c != "" {
		settings = append(settings, CapacitySetting(c))
	}
	// Red stub R3 (section 5 s2, review 5969268537 F4): the JSON of the PVC spec.
	settings = []string{redFragment(e.obj.Object["spec"])}
	s.add(BridgePVCPV, e, targetKey{"", "PersistentVolume", "", spec.VolumeName}, settings...)
}

type ingressBackend struct {
	Service *struct {
		Name string `json:"name"`
		Port struct {
			Number int    `json:"number"`
			Name   string `json:"name"`
		} `json:"port"`
	} `json:"service"`
}

// ingress makes the Ingress -> Service bridges: each path with a Service backend in spec
// order, then the default backend.
func (s *bridgeSet) ingress(e *entry) {
	var spec struct {
		DefaultBackend *ingressBackend `json:"defaultBackend"`
		Rules          []struct {
			Host string `json:"host"`
			HTTP *struct {
				Paths []struct {
					Path     string         `json:"path"`
					PathType string         `json:"pathType"`
					Backend  ingressBackend `json:"backend"`
				} `json:"paths"`
			} `json:"http"`
		} `json:"rules"`
	}
	field(e, "spec", &spec)
	port := func(b ingressBackend) string {
		if b.Service.Port.Number != 0 {
			return strconv.Itoa(b.Service.Port.Number)
		}
		return b.Service.Port.Name
	}
	for _, r := range spec.Rules {
		if r.HTTP == nil {
			continue
		}
		for _, p := range r.HTTP.Paths {
			if p.Backend.Service == nil || p.Backend.Service.Name == "" {
				continue
			}
			s.add(BridgeIngressService, e, targetKey{"", "Service", s.c.ns, p.Backend.Service.Name},
				IngressSetting(p.Path, p.PathType, r.Host, p.Backend.Service.Name, port(p.Backend)))
		}
	}
	if b := spec.DefaultBackend; b != nil && b.Service != nil && b.Service.Name != "" {
		s.add(BridgeIngressService, e, targetKey{"", "Service", s.c.ns, b.Service.Name}, DefaultBackendSetting(b.Service.Name, port(*b)))
	}
}

// hpa makes the HPA -> target bridge, in the group of scaleTargetRef.apiVersion (B5).
func (s *bridgeSet) hpa(e *entry) {
	var spec struct {
		ScaleTargetRef struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			Name       string `json:"name"`
		} `json:"scaleTargetRef"`
		MinReplicas *int `json:"minReplicas"`
		MaxReplicas int  `json:"maxReplicas"`
	}
	field(e, "spec", &spec)
	if spec.ScaleTargetRef.Name == "" {
		return
	}
	g, _ := splitAPIVersion(spec.ScaleTargetRef.APIVersion)
	s.add(BridgeHPATarget, e, targetKey{g, spec.ScaleTargetRef.Kind, s.c.ns, spec.ScaleTargetRef.Name},
		MinSetting(spec.MinReplicas), MaxSetting(spec.MaxReplicas))
}

// route makes the HTTPRoute -> Service bridges of a route read at a named version: for
// each rule, each match (none: PathPrefix /) and each backendRef of the core group and
// kind Service, in that order. A backendRef of another group or kind gives no bridge, and
// one of another namespace is never joined to an object of this one (B5, BT-5). In this
// namespace, a refused Services LIST makes every target not checkable, with to.uid null
// even when an owner lookup of the call read that Service (BT-4, B3); otherwise a target
// that is not a node is "not found" (BT-3).
func (s *bridgeSet) route(e *entry) {
	var spec struct {
		Hostnames []string `json:"hostnames"`
		Rules     []struct {
			Matches []struct {
				Path *struct {
					Type  *string `json:"type"`
					Value *string `json:"value"`
				} `json:"path"`
			} `json:"matches"`
			BackendRefs []struct {
				Group     *string `json:"group"`
				Kind      *string `json:"kind"`
				Name      string  `json:"name"`
				Namespace *string `json:"namespace"`
				Port      *int    `json:"port"`
			} `json:"backendRefs"`
		} `json:"rules"`
	}
	field(e, "spec", &spec)
	type match struct{ typ, path string }
	for _, r := range spec.Rules {
		var matches []match
		for _, m := range r.Matches {
			mt := match{"PathPrefix", "/"}
			if m.Path != nil && m.Path.Type != nil {
				mt.typ = *m.Path.Type
			}
			if m.Path != nil && m.Path.Value != nil {
				mt.path = *m.Path.Value
			}
			matches = append(matches, mt)
		}
		if len(matches) == 0 {
			matches = []match{{"PathPrefix", "/"}}
		}
		for _, m := range matches {
			for _, b := range r.BackendRefs {
				if (b.Group != nil && *b.Group != "") || (b.Kind != nil && *b.Kind != "Service") || b.Name == "" {
					continue
				}
				ns := s.c.ns
				if b.Namespace != nil && *b.Namespace != "" {
					ns = *b.Namespace
				}
				t := targetKey{"", "Service", ns, b.Name}
				br := s.add(BridgeHTTPRouteService, e, t, RouteSetting(m.path, m.typ, spec.Hostnames, s.c.ns, ns, b.Name, b.Port))
				_, found := s.nodes[t]
				switch {
				case ns != s.c.ns:
					br.Text = OtherNamespaceText(ns, b.Name)
				case s.c.servicesRefused:
					br.To.UID = nil
					br.Text = ServicesForbiddenText
				case found:
				default:
					br.Text = ServiceNotFoundText(b.Name)
				}
			}
		}
	}
}

// The settings strings of packet FA2-T6 section 3.1 are made only by the functions below
// (E.5). U+2192 and U+2190 are the arrows of the screen's reference lines.
const (
	rightArrow = "\u2192"
	leftArrow  = "\u2190"
)

// IngressSetting is one Ingress path: <path><type>, two spaces, <host> U+2192
// <service>:<port>.
func IngressSetting(path, pathType, host, service, port string) string {
	if path == "" {
		path = "/"
	}
	if pathType != "" && pathType != "Prefix" {
		path += " (" + pathType + ")"
	}
	if host == "" {
		host = "*"
	}
	return fmt.Sprintf("%s  %s %s %s:%s", path, host, rightArrow, service, port)
}

// DefaultBackendSetting is the default backend of an Ingress.
func DefaultBackendSetting(service, port string) string {
	return fmt.Sprintf("(default)  * %s %s:%s", rightArrow, service, port)
}

// RouteSetting is one (match, backendRef) of an HTTPRoute rule; a target in another
// namespace is written <namespace>/<name>, and the port is left out when the backendRef
// has none.
func RouteSetting(path, matchType string, hostnames []string, namespace, targetNamespace, service string, port *int) string {
	if matchType != "PathPrefix" {
		path += " (" + matchType + ")"
	}
	hosts := strings.Join(hostnames, ",")
	if hosts == "" {
		hosts = "*"
	}
	if targetNamespace != namespace {
		service = targetNamespace + "/" + service
	}
	if port != nil {
		service += ":" + strconv.Itoa(*port)
	}
	return fmt.Sprintf("%s  %s %s %s", path, hosts, rightArrow, service)
}

// SelectorSetting is a Service selector, pairs sorted by key.
func SelectorSetting(selector map[string]string) string {
	var pairs []string
	for k, v := range selector {
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	return "selector " + strings.Join(pairs, ",")
}

// PortSetting is one Service port, :<port> U+2192 <targetPort>, with /<protocol> when not
// TCP.
func PortSetting(port int, target, protocol string) string {
	s := fmt.Sprintf(":%d%s%s", port, rightArrow, target)
	if protocol != "" && protocol != "TCP" {
		s += "/" + protocol
	}
	return s
}

// ClaimSetting is the claim of a Pod -> PVC bridge.
func ClaimSetting(claim string) string { return "volume " + claim }

// MountSetting is one mount of a claim.
func MountSetting(container, path string, readOnly bool) string {
	s := "mount " + container + ":" + path
	if readOnly {
		s += " (read-only)"
	}
	return s
}

// AccessSetting is the access modes of a PVC that is a node.
func AccessSetting(modes []string) string { return "access " + strings.Join(modes, ",") }

// ClassSetting is the StorageClass of a PVC: unset is "(default)", empty is "(none)".
func ClassSetting(class *string) string {
	switch {
	case class == nil:
		return "class (default)"
	case *class == "":
		return "class (none)"
	}
	return "class " + *class
}

// CapacitySetting is the capacity of a bound PVC as its status gives it.
func CapacitySetting(capacity string) string { return "capacity " + capacity }

// EnvSetting is an env entry that takes a key of a ConfigMap or Secret.
func EnvSetting(container, name, key string) string {
	return fmt.Sprintf("env %s:%s %s %s", container, name, leftArrow, key)
}

// EnvFromSetting is an envFrom entry.
func EnvFromSetting(container, prefix string) string {
	s := "envFrom " + container
	if prefix != "" {
		s += " prefix " + prefix
	}
	return s
}

// VolumeSetting is a mounted ConfigMap or Secret volume or projected source, with its
// item keys in spec order, or all keys.
func VolumeSetting(container, path string, keys []string) string {
	s := "volume " + container + ":" + path
	if len(keys) == 0 {
		return s + " (all keys)"
	}
	return s + ": " + strings.Join(keys, ", ")
}

// UnmountedSetting is a ConfigMap or Secret volume that no container mounts.
func UnmountedSetting(volume string) string { return "volume " + volume + " (not mounted)" }

// MinSetting and MaxSetting are the replica bounds of an HPA; min is 1 when unset.
func MinSetting(min *int) string {
	n := 1
	if min != nil {
		n = *min
	}
	return "min " + strconv.Itoa(n)
}

func MaxSetting(max int) string { return "max " + strconv.Itoa(max) }
