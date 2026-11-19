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
				c.listedAs(k, false)
				if k.kind == "Service" {
					c.servicesRefused = true
				}
				continue
			}
			return c.listFail(ctx, err, k.kind)
		}
		c.listedAs(k, true)
		if k.kind == "PersistentVolumeClaim" && len(list.Items) > 0 {
			c.claimsListed = true
		}
		for i := range list.Items {
			e := c.add(&list.Items[i], k)
			e.state, e.parent, e.ref, e.text, e.placed = StateNone, "", "", "", true
		}
	}
	return nil
}

// listFail is the failure of a namespaced LIST of kind other than 403 (FA2-T7a 3.5): the
// call fails with the kind named, except a stopped call, the end of the call's context and
// a failure to open the operation, which keep the FA2-T5 answers.
func (c *call) listFail(ctx context.Context, err error, kind string) error {
	var le *LoadError
	if errors.Is(err, ErrStopped) || errors.As(err, &le) || ctx.Err() != nil {
		return c.fail(err)
	}
	return &LoadError{Namespace: c.ns, Reason: reasonOf(err), Kind: kind}
}

// targetKey identifies an object by group, kind, namespace and name (B5); namespace is ""
// for a cluster-scoped object.
type targetKey struct{ group, kind, namespace, name string }

// stateCheck is the state of one settings entry of a bridge and the text of that state.
type stateCheck struct{ state, text string }

var connected = stateCheck{BridgeConnected, ""}

// bridgeSet collects the bridges of one call, one per (kind, from, target), with their
// settings in the order they are found. checks holds, per bridge, the function that
// gives the state of each settings entry once every bridge exists (FA2-T7a 6.4).
type bridgeSet struct {
	c      *call
	nodes  map[targetKey]string // the uid of each node
	index  map[string]int       // "<kind> <from uid> <target>" -> position in list
	list   []Bridge
	checks []func(b *Bridge) []stateCheck
}

// add appends settings to the bridge of kind from e to t, making it on first use with the
// state function of its kind.
func (s *bridgeSet) add(kind string, e *entry, t targetKey, state func(b *Bridge) []stateCheck, settings ...string) *Bridge {
	key := fmt.Sprintf("%s %s %s/%s/%s/%s", kind, e.uid, t.group, t.kind, t.namespace, t.name)
	i, ok := s.index[key]
	if !ok {
		to := Target{Group: t.group, Namespace: t.namespace, Kind: t.kind, Name: t.name}
		if uid, found := s.nodes[t]; found && t.name != "" {
			to.UID = &uid
		}
		s.list = append(s.list, Bridge{Kind: kind, From: From{UID: e.uid}, To: to, Settings: []string{}})
		s.checks = append(s.checks, state)
		i = len(s.list) - 1
		s.index[key] = i
	}
	s.list[i].Settings = append(s.list[i].Settings, settings...)
	return &s.list[i]
}

// node is the entry of the node t, or nil.
func (s *bridgeSet) node(t targetKey) *entry {
	if uid, ok := s.nodes[t]; ok {
		return s.c.entries[uid]
	}
	return nil
}

// bridges makes the bridges of the answer from the objects already read, without any
// further request (E.3, E.4, FA2-T7a 6.3 (9)), the unsupported entry of an HTTPRoute
// resource read at a version that is not named (B4), and then the state of every bridge
// (6.4). Bridges are in the order of B6.
func (c *call) bridges(ctx context.Context, a *Answer) error {
	s := &bridgeSet{c: c, nodes: map[targetKey]string{}, index: map[string]int{}}
	for _, e := range c.entries {
		g, _ := splitAPIVersion(e.apiVersion)
		ns := c.ns
		if e.cluster {
			ns = ""
		}
		// A node of a kind whose LIST was refused is never a bridge target, even when an
		// owner lookup of the call read it: the refusal stands (C15, B3, 3.1).
		if !e.cluster && c.refusedKind(g, e.kind) {
			continue
		}
		s.nodes[targetKey{g, e.kind, ns, e.name}] = e.uid
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
			s.claim(e, entries)
		case g == "" && e.kind == "PersistentVolume":
			s.volume(e)
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
	for i := range s.list {
		c.state(&s.list[i], s.checks[i](&s.list[i]))
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

// severity orders the bridge states as UI.md line 56: broken first, connected last.
var severity = map[string]int{BridgeBroken: 0, BridgeWarning: 1, BridgeUnknown: 2, BridgeWaiting: 3, BridgeOptional: 4, BridgeConnected: 5}

// worst is the most severe state of checks and the text of the first check, in settings
// order, that has it (FA2-T7a 3.1).
func worst(checks []stateCheck) stateCheck {
	w := connected
	for _, ch := range checks {
		if severity[ch.state] < severity[w.state] {
			w = ch
		}
	}
	return w
}

// state sets the state of b and its text, present exactly when the state is not
// connected (FA2-T7a 3.1).
func (c *call) state(b *Bridge, checks []stateCheck) {
	w := worst(checks)
	// Red stub (t1, packet FA2-T7a section 7): every bridge is connected; the text is the
	// final one.
	b.State, b.Text = BridgeConnected, w.text
}

// field decodes the top-level field name of e's object (spec, status) into v. A Pod
// container's env values are never decoded: the types below have no field for them (B2).
func field(e *entry, name string, v any) {
	decode(e.obj.Object[name], v)
}

// decode converts a part of an object into v through JSON.
func decode(part, v any) {
	raw, err := json.Marshal(part)
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
	Ports []struct {
		Name          string `json:"name"`
		ContainerPort int    `json:"containerPort"`
		Protocol      string `json:"protocol"`
	} `json:"ports"`
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

// claimsOf are the claims a Pod names, in volume order: a persistentVolumeClaim volume
// names its claim, a generic ephemeral volume the claim <pod>-<volume> (E.4).
func claimsOf(e *entry) (claims []string, ephemeral map[string]bool) {
	var spec podSpec
	field(e, "spec", &spec)
	ephemeral = map[string]bool{}
	for _, v := range spec.Volumes {
		switch {
		case v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName != "":
			claims = append(claims, v.PersistentVolumeClaim.ClaimName)
		case v.Ephemeral != nil:
			claims = append(claims, e.name+"-"+v.Name)
			ephemeral[e.name+"-"+v.Name] = true
		}
	}
	return claims, ephemeral
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
		b := s.add(bk, e, targetKey{"", kind, s.c.ns, name}, s.configState, setting)
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
	_, ephemeral := claimsOf(e)
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
		claim, pod := claim, e.name
		state := func(b *Bridge) []stateCheck {
			switch {
			case s.node(t) != nil:
				return []stateCheck{connected}
			case s.c.refusedKind("", "PersistentVolumeClaim"):
				return []stateCheck{{BridgeUnknown, NoPermissionText("PersistentVolumeClaim")}}
			case ephemeral[claim]:
				return []stateCheck{{BridgeWaiting, EphemeralWaitText(claim, pod)}}
			}
			return []stateCheck{{BridgeBroken, NotFoundText("PersistentVolumeClaim", claim)}}
		}
		b := s.add(BridgePodPVC, e, t, state, ClaimSetting(claim))
		for _, m := range claimMounts[claim] {
			b.Settings = append(b.Settings, MountSetting(m.container, m.path, m.readOnly))
		}
		if pvc := s.node(t); pvc != nil && pvc.obj != nil {
			var cs struct {
				AccessModes []string `json:"accessModes"`
			}
			field(pvc, "spec", &cs)
			if len(cs.AccessModes) > 0 {
				b.Settings = append(b.Settings, AccessSetting(cs.AccessModes))
			}
		}
	}
}

// configState is the state of each settings entry of a Pod -> ConfigMap or Pod -> Secret
// bridge (C-1 to C-4): a missing target is optional only when every reference to it is.
func (s *bridgeSet) configState(b *Bridge) []stateCheck {
	t := targetKey{"", b.To.Kind, s.c.ns, b.To.Name}
	ch := connected
	switch {
	case s.node(t) != nil:
	case s.c.refusedKind("", b.To.Kind):
		ch = stateCheck{BridgeUnknown, NoPermissionText(b.To.Kind)}
	default:
		ch = stateCheck{BridgeOptional, NotFoundText(b.To.Kind, b.To.Name)}
		for _, o := range b.Optional {
			if !o {
				ch.state = BridgeBroken
			}
		}
	}
	out := make([]stateCheck, len(b.Settings))
	for i := range out {
		out[i] = ch
	}
	return out
}

// servicePort is one port of a Service: its number, its target as written (a name, a
// number, or unset, which is the port) and its protocol.
type servicePort struct {
	Port       int             `json:"port"`
	Name       string          `json:"name"`
	TargetPort json.RawMessage `json:"targetPort"`
	Protocol   string          `json:"protocol"`
}

// target is the targetPort of p: a name, or a number (the port when unset).
func (p servicePort) target() (name string, number int) {
	if json.Unmarshal(p.TargetPort, &name) == nil && name != "" {
		return name, 0
	}
	if json.Unmarshal(p.TargetPort, &number) == nil && number != 0 {
		return "", number
	}
	return "", p.Port
}

// protocol is the protocol of a port; unset is TCP.
func protocol(p string) string {
	if p == "" {
		return "TCP"
	}
	return p
}

// service makes one Service -> Pod bridge per Pod of the answer whose labels hold every
// pair of the Service's selector, and one bridge to a Pod with an empty name when no Pod
// does (S-2, S-5); an ExternalName Service and one without selector give none (E.4).
func (s *bridgeSet) service(e *entry, entries []*entry) {
	var spec struct {
		Type     string            `json:"type"`
		Selector map[string]string `json:"selector"`
		Ports    []servicePort     `json:"ports"`
	}
	field(e, "spec", &spec)
	if len(spec.Selector) == 0 || spec.Type == "ExternalName" {
		return
	}
	settings := []string{SelectorSetting(spec.Selector)}
	for _, p := range spec.Ports {
		name, number := p.target()
		target := name
		if name == "" {
			target = strconv.Itoa(number)
		}
		settings = append(settings, PortSetting(p.Port, target, p.Protocol))
	}
	matched := false
	for _, p := range entries {
		// With the Pods LIST refused, no Pod read by an owner lookup is matched (S-5).
		if g, _ := splitAPIVersion(p.apiVersion); g != "" || p.kind != "Pod" || p.obj == nil || s.c.refusedKind("", "Pod") {
			continue
		}
		labels := p.obj.GetLabels()
		match := true
		for k, v := range spec.Selector {
			if l, ok := labels[k]; !ok || l != v {
				match = false
			}
		}
		if !match {
			continue
		}
		matched = true
		pod := p
		state := func(b *Bridge) []stateCheck {
			var podSpec podSpec
			field(pod, "spec", &podSpec)
			out := []stateCheck{connected}
			for _, port := range spec.Ports {
				out = append(out, portCheck(port, append(append([]podContainer{}, podSpec.Containers...), podSpec.InitContainers...)))
			}
			return out
		}
		s.add(BridgeServicePod, e, targetKey{"", "Pod", s.c.ns, p.name}, state, settings...)
	}
	// Red stub (t1): no bridge for a selector that matches no Pod.
	if !matched && false {
		selector := strings.TrimPrefix(SelectorSetting(spec.Selector), "selector ")
		state := func(b *Bridge) []stateCheck {
			if s.c.refusedKind("", "Pod") {
				return []stateCheck{{BridgeUnknown, NoPermissionText("Pod")}}
			}
			return []stateCheck{{BridgeBroken, SelectorNoPodsText(selector)}}
		}
		s.add(BridgeServicePod, e, targetKey{"", "Pod", s.c.ns, ""}, state, settings...)
	}
}

// portCheck resolves one Service port against the containers of one Pod (S-3, S-4): a
// named targetPort needs a container port of that name, a numeric one a container port
// of that number and protocol.
func portCheck(p servicePort, containers []podContainer) stateCheck {
	name, number := p.target()
	for _, ct := range containers {
		for _, cp := range ct.Ports {
			if name != "" && cp.Name == name {
				return connected
			}
			if name == "" && cp.ContainerPort == number && protocol(cp.Protocol) == protocol(p.Protocol) {
				return connected
			}
		}
	}
	if name != "" {
		return stateCheck{BridgeBroken, NamedPortText(name)}
	}
	return stateCheck{BridgeWarning, NumericPortText(number)}
}

// claim makes the PVC -> PV bridge of a claim, to an empty PV name when it has no volume
// yet (V-1 to V-8), and its PVC -> StorageClass bridge when it names a class (K-1 to K-4);
// the PV and the class are read by the call only as cluster objects (6.3 (7)).
func (s *bridgeSet) claim(e *entry, entries []*entry) {
	var spec struct {
		VolumeName       string  `json:"volumeName"`
		StorageClassName *string `json:"storageClassName"`
	}
	var status struct {
		Phase    string            `json:"phase"`
		Capacity map[string]string `json:"capacity"`
	}
	field(e, "spec", &spec)
	field(e, "status", &status)
	class := ""
	if spec.StorageClassName != nil {
		class = *spec.StorageClassName
	}
	// A claim read only by an owner lookup while the claims LIST was refused: the call
	// listed no PersistentVolume or StorageClass (6.3 (7)), so neither bridge is checkable.
	refused := stateCheck{BridgeUnknown, NoPermissionText("PersistentVolumeClaim")}
	claimsRefused := s.c.refusedKind("", "PersistentVolumeClaim")
	// Red stub (t1): no PVC -> PV bridge for a claim without a volume.
	if spec.VolumeName != "" {
		settings := []string{ClassSetting(spec.StorageClassName)}
		if c := status.Capacity["storage"]; c != "" {
			settings = append(settings, CapacitySetting(c))
		}
		pvc, volume := e.name, spec.VolumeName
		state := func(b *Bridge) []stateCheck {
			if claimsRefused {
				return []stateCheck{refused}
			}
			if volume != "" {
				switch {
				case s.node(targetKey{"", "PersistentVolume", "", volume}) != nil:
					return []stateCheck{connected}
				case s.c.pvRefused:
					return []stateCheck{{BridgeUnknown, NoPermissionText("PersistentVolume")}}
				}
				return []stateCheck{{BridgeBroken, NotFoundText("PersistentVolume", volume)}}
			}
			if class == "" {
				return []stateCheck{{BridgeWaiting, VolumeWaitText(pvc)}}
			}
			sc := s.c.classes[class]
			switch {
			case s.c.classRefused:
				return []stateCheck{{BridgeUnknown, NoPermissionText("StorageClass")}}
			case sc == nil:
				return []stateCheck{{BridgeBroken, ClassUnboundText(class)}}
			}
			var mode struct {
				VolumeBindingMode string `json:"volumeBindingMode"`
			}
			decode(sc.Object, &mode)
			if mode.VolumeBindingMode != "WaitForFirstConsumer" {
				return []stateCheck{{BridgeWaiting, ProvisionWaitText(pvc)}}
			}
			for _, p := range entries {
				if g, _ := splitAPIVersion(p.apiVersion); g != "" || p.kind != "Pod" || p.obj == nil {
					continue
				}
				claims, _ := claimsOf(p)
				for _, cl := range claims {
					if cl == pvc {
						return []stateCheck{{BridgeWaiting, ConsumerPodWaitText(pvc, p.name)}}
					}
				}
			}
			return []stateCheck{{BridgeWaiting, ConsumerWaitText(pvc)}}
		}
		s.add(BridgePVCPV, e, targetKey{"", "PersistentVolume", "", spec.VolumeName}, state, settings...)
	}
	if class != "" {
		bound := status.Phase == "Bound"
		state := s.classState(class, bound)
		if claimsRefused {
			state = func(b *Bridge) []stateCheck { return []stateCheck{refused} }
		}
		s.add(BridgePVCStorageClass, e, targetKey{"storage.k8s.io", "StorageClass", "", class}, state, ClassSetting(&class))
	}
}

// volume makes the PV -> StorageClass bridge of a PV node that names a class.
func (s *bridgeSet) volume(e *entry) {
	var spec struct {
		StorageClassName string `json:"storageClassName"`
	}
	field(e, "spec", &spec)
	if class := spec.StorageClassName; class != "" {
		s.add(BridgePVStorageClass, e, targetKey{"storage.k8s.io", "StorageClass", "", class}, s.classState(class, true), ClassSetting(&class))
	}
}

// classState is K-1 to K-4: a missing class is broken only for a claim that is not bound.
func (s *bridgeSet) classState(class string, bound bool) func(b *Bridge) []stateCheck {
	return func(b *Bridge) []stateCheck {
		switch {
		case s.c.classes[class] != nil:
			return []stateCheck{connected}
		case s.c.classRefused:
			return []stateCheck{{BridgeUnknown, NoPermissionText("StorageClass")}}
		case bound:
			return []stateCheck{{BridgeWarning, ClassBoundText(class)}}
		}
		return []stateCheck{{BridgeBroken, ClassUnboundText(class)}}
	}
}

// servicePortCheck is I-1, I-3 and I-4 for a Service of the answer: a port number must be
// among its ports' numbers, a port name among their names.
func (s *bridgeSet) servicePortCheck(svc *entry, number int, name string) stateCheck {
	if svc.obj == nil {
		return connected
	}
	var spec struct {
		Ports []servicePort `json:"ports"`
	}
	field(svc, "spec", &spec)
	for _, p := range spec.Ports {
		if (name != "" && p.Name == name) || (name == "" && p.Port == number) {
			return connected
		}
	}
	if name != "" {
		return stateCheck{BridgeBroken, ServicePortNameText(svc.name, name)}
	}
	return stateCheck{BridgeBroken, ServicePortText(svc.name, number)}
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
// order, then the default backend (I-1 to I-4, I-6).
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
	ports := map[targetKey][]ingressBackend{} // the backends of each bridge, in settings order
	backend := func(b ingressBackend, setting string) {
		t := targetKey{"", "Service", s.c.ns, b.Service.Name}
		s.add(BridgeIngressService, e, t, func(br *Bridge) []stateCheck {
			var out []stateCheck
			for _, pb := range ports[t] {
				svc := s.node(t)
				switch {
				case s.c.servicesRefused:
					br.To.UID = nil
					out = append(out, stateCheck{BridgeUnknown, ServicesForbiddenText})
				case svc == nil:
					out = append(out, stateCheck{BridgeBroken, ServiceNotFoundText(t.name)})
				default:
					out = append(out, s.servicePortCheck(svc, pb.Service.Port.Number, pb.Service.Port.Name))
				}
			}
			return out
		}, setting)
		ports[t] = append(ports[t], b)
	}
	for _, r := range spec.Rules {
		if r.HTTP == nil {
			continue
		}
		for _, p := range r.HTTP.Paths {
			if p.Backend.Service == nil || p.Backend.Service.Name == "" {
				continue
			}
			backend(p.Backend, IngressSetting(p.Path, p.PathType, r.Host, p.Backend.Service.Name, port(p.Backend)))
		}
	}
	if b := spec.DefaultBackend; b != nil && b.Service != nil && b.Service.Name != "" {
		backend(*b, DefaultBackendSetting(b.Service.Name, port(*b)))
	}
}

// hpa makes the HPA -> target bridge, in the group of scaleTargetRef.apiVersion (B5, H-1
// to H-4).
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
	t := targetKey{g, spec.ScaleTargetRef.Kind, s.c.ns, spec.ScaleTargetRef.Name}
	state := func(b *Bridge) []stateCheck {
		switch listed, known := s.c.lists[g+"/"+t.kind]; {
		case s.node(t) != nil:
			return []stateCheck{connected}
		case known && !listed:
			return []stateCheck{{BridgeUnknown, NoPermissionText(t.kind)}}
		case known:
			return []stateCheck{{BridgeBroken, NotFoundText(t.kind, t.name)}}
		case g != "" && !s.c.served[g]:
			return []stateCheck{{BridgeUnknown, NotServedText(g, t.kind)}}
		}
		return []stateCheck{{BridgeUnknown, NotReadText(t.kind, t.name)}}
	}
	s.add(BridgeHPATarget, e, t, state, MinSetting(spec.MinReplicas), MaxSetting(spec.MaxReplicas))
}

// route makes the HTTPRoute -> Service bridges of a route read at a named version: for
// each rule, each match (none: PathPrefix /) and each backendRef of the core group and
// kind Service, in that order. A backendRef of another group or kind gives no bridge, and
// one of another namespace is never joined to an object of this one (B5, BT-5). In this
// namespace, a refused Services LIST makes every target not checkable, with to.uid null
// even when an owner lookup of the call read that Service (BT-4, B3); otherwise a target
// that is not a node is "not found" (BT-3), and one that is a node has its port checked
// (I-1, I-3, I-4).
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
	ports := map[targetKey][]*int{} // the backendRef ports of each bridge, in settings order
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
				s.add(BridgeHTTPRouteService, e, t, func(br *Bridge) []stateCheck {
					var out []stateCheck
					for _, port := range ports[t] {
						svc := s.node(t)
						switch {
						case t.namespace != s.c.ns:
							out = append(out, stateCheck{BridgeUnknown, OtherNamespaceText(t.namespace, t.name)})
						case s.c.servicesRefused:
							br.To.UID = nil
							out = append(out, stateCheck{BridgeUnknown, ServicesForbiddenText})
						case svc == nil:
							out = append(out, stateCheck{BridgeBroken, ServiceNotFoundText(t.name)})
						case port == nil:
							out = append(out, connected)
						default:
							out = append(out, s.servicePortCheck(svc, *port, ""))
						}
					}
					return out
				}, RouteSetting(m.path, m.typ, spec.Hostnames, s.c.ns, ns, b.Name, b.Port))
				ports[t] = append(ports[t], b.Port)
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
