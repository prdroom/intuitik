package graph

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

// builtInGroups is list B of packet D.2: the groups that kube-apiserver v1.37.0 registers
// itself, and the metrics aggregation groups. Every other group of /apis is read through
// its discovery documents, so its namespaced resources are listed like custom resources.
var builtInGroups = map[string]bool{
	"":                             true,
	"admissionregistration.k8s.io": true,
	"apiextensions.k8s.io":         true,
	"apiregistration.k8s.io":       true,
	"apps":                         true,
	"authentication.k8s.io":        true,
	"authorization.k8s.io":         true,
	"autoscaling":                  true,
	"batch":                        true,
	"certificates.k8s.io":          true,
	"coordination.k8s.io":          true,
	"discovery.k8s.io":             true,
	"events.k8s.io":                true,
	"flowcontrol.apiserver.k8s.io": true,
	"internal.apiserver.k8s.io":    true,
	"lifecycle.k8s.io":             true,
	"networking.k8s.io":            true,
	"node.k8s.io":                  true,
	"policy":                       true,
	"rbac.authorization.k8s.io":    true,
	"resource.k8s.io":              true,
	"scheduling.k8s.io":            true,
	"storage.k8s.io":               true,
	"storagemigration.k8s.io":      true,
	"metrics.k8s.io":               true,
	"custom.metrics.k8s.io":        true,
	"external.metrics.k8s.io":      true,
}

// BuiltInGroup reports a group of list B.
func BuiltInGroup(group string) bool { return builtInGroups[group] }

// listedKind is one kind that a call lists in the namespace.
type listedKind struct {
	res  readonly.Resource
	kind string
}

// builtInKinds are the seven built-in kinds of packet D.2, in the order of D.3.
var builtInKinds = []listedKind{
	{readonly.Resource{Version: "v1", Resource: "pods"}, "Pod"},
	{readonly.Resource{Group: "apps", Version: "v1", Resource: "replicasets"}, "ReplicaSet"},
	{readonly.Resource{Group: "apps", Version: "v1", Resource: "deployments"}, "Deployment"},
	{readonly.Resource{Group: "apps", Version: "v1", Resource: "statefulsets"}, "StatefulSet"},
	{readonly.Resource{Group: "apps", Version: "v1", Resource: "daemonsets"}, "DaemonSet"},
	{readonly.Resource{Group: "batch", Version: "v1", Resource: "jobs"}, "Job"},
	{readonly.Resource{Group: "batch", Version: "v1", Resource: "cronjobs"}, "CronJob"},
}

// documentLimit bounds the bytes read of one discovery document.
const documentLimit = 16 << 20

// document is one discovery document of a call: its resources, or what its read gave.
type document struct {
	list   *metav1.APIResourceList
	failed bool
	status int    // the HTTP status received; 0 for no answer
	reason string // the status number or NoAnswer
	needed bool   // an owner decision of the call used this document
}

// fetched is one owner GET of a call.
type fetched struct {
	obj *unstructured.Unstructured
	err error
}

// entry is one node of the answer while the call builds it.
type entry struct {
	uid, apiVersion, kind, name string
	refs                        []metav1.OwnerReference
	state                       State
	parent                      string
	ref, text                   string
	placed                      bool
}

// call is one graph call: its caches live only as long as the call (G5).
type call struct {
	open    Open
	ns      string
	docs    map[string]*document // "<group>/<version>", "/v1" for the core group
	owners  map[string]*fetched  // "<group>/<version>/<resource>/<namespace>/<name>"
	entries map[string]*entry    // by uid
}

// Build reads the containment of namespace through open (packet D.1 to D.3). The cluster
// operations run one after another in the fixed order of D.3, each on its own client. An
// error is ErrStopped (or another error of open), the context's error, or a *LoadError.
func Build(ctx context.Context, open Open, namespace string) (Answer, error) {
	c := &call{open: open, ns: namespace, docs: map[string]*document{}, owners: map[string]*fetched{}, entries: map[string]*entry{}}
	groups, err := c.groups(ctx)
	if err != nil {
		return Answer{}, err
	}
	// (2) The served version documents of each group outside list B, in name order and
	// version order.
	for _, g := range groups {
		for _, v := range g.versions {
			if _, err := c.document(ctx, g.name, v); err != nil {
				return Answer{}, err
			}
		}
	}
	// (3) The LISTs: the seven built-in kinds, then the other kinds by group and kind.
	refused := []Refusal{}
	kinds := append(append([]listedKind{}, builtInKinds...), c.otherKinds(groups)...)
	for _, k := range kinds {
		list, err := c.list(ctx, k.res)
		if err != nil {
			if code, ok := statusCode(err); ok && code == http.StatusForbidden {
				refused = append(refused, Refusal{Kind: k.kind, Status: code, Reason: strconv.Itoa(code), Text: KindText(k.kind, code)})
				continue
			}
			return Answer{}, c.fail(err)
		}
		for i := range list.Items {
			c.add(&list.Items[i], k)
		}
	}
	// (4) The owner chain of each listed object, in the order of G6.
	for _, e := range c.sorted() {
		if err := c.place(ctx, e, nil); err != nil {
			return Answer{}, err
		}
	}
	a := Answer{Namespace: namespace, Nodes: []Node{}, Refused: refused}
	for _, e := range c.sorted() {
		n := Node{UID: e.uid, APIVersion: e.apiVersion, Kind: e.kind, Name: e.name, Owner: Owner{State: e.state, Reference: e.ref, Text: e.text}}
		if e.parent != "" {
			p := e.parent
			n.Parent = &p
		}
		a.Nodes = append(a.Nodes, n)
	}
	// A document that no owner decision needed is reported with what was received; one
	// that a decision needed has already given owner-missing (404) or failed the call.
	var keys []string
	for k, d := range c.docs {
		if d.failed && !d.needed {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		d := c.docs[k]
		group, version, _ := strings.Cut(k, "/")
		a.Refused = append(a.Refused, Refusal{Group: group, Version: version, Status: d.status, Reason: d.reason, Text: GroupText(group, version, d.reason)})
	}
	return a, nil
}

// group is one group of /apis outside list B with its served versions in version order.
type group struct {
	name      string
	preferred string
	versions  []string
}

// reader opens the client of one operation.
func (c *call) reader(ctx context.Context) (Reader, error) {
	if ctx.Err() != nil {
		return nil, &LoadError{Namespace: c.ns, Reason: NoAnswer}
	}
	return c.open(ctx)
}

// fail turns the error of a request into the failure of the whole call. ErrStopped stays
// as it is: the call ends with the gate's answer, not with a load error.
func (c *call) fail(err error) error {
	if errors.Is(err, ErrStopped) {
		return err
	}
	return &LoadError{Namespace: c.ns, Reason: reasonOf(err)}
}

// groups reads /apis (D.3 (1)). A failure fails the call: no other kind can be found.
func (c *call) groups(ctx context.Context) ([]group, error) {
	r, err := c.reader(ctx)
	if err != nil {
		return nil, err
	}
	status, body, err := get(ctx, r, "/apis")
	if err != nil {
		return nil, c.fail(err)
	}
	var list metav1.APIGroupList
	if status != http.StatusOK {
		return nil, &LoadError{Namespace: c.ns, Reason: strconv.Itoa(status)}
	}
	if json.Unmarshal(body, &list) != nil {
		return nil, &LoadError{Namespace: c.ns, Reason: NoAnswer}
	}
	var out []group
	for _, g := range list.Groups {
		if builtInGroups[g.Name] {
			continue
		}
		gr := group{name: g.Name, preferred: g.PreferredVersion.Version}
		for _, v := range g.Versions {
			gr.versions = append(gr.versions, v.Version)
		}
		sort.SliceStable(gr.versions, func(i, j int) bool { return versionBefore(gr.versions[i], gr.versions[j]) })
		out = append(out, gr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// document reads the discovery document of one group version once per call. A failed
// read is kept in the document; the only error is one of the call's own end.
func (c *call) document(ctx context.Context, group, version string) (*document, error) {
	key := group + "/" + version
	if d, ok := c.docs[key]; ok {
		return d, nil
	}
	r, err := c.reader(ctx)
	if err != nil {
		return nil, err
	}
	path := "/apis/" + group + "/" + version
	if group == "" {
		path = "/api/" + version
	}
	d := &document{}
	status, body, err := get(ctx, r, path)
	var list metav1.APIResourceList
	switch {
	case err != nil:
		d.failed, d.reason = true, reasonOf(err)
		if code, ok := statusCode(err); ok {
			d.status = code
		}
	case status != http.StatusOK:
		d.failed, d.status, d.reason = true, status, strconv.Itoa(status)
	case json.Unmarshal(body, &list) != nil:
		d.failed, d.reason = true, NoAnswer
	default:
		d.list = &list
	}
	c.docs[key] = d
	return d, nil
}

// otherKinds are the namespaced resources of the groups outside list B that can be
// listed, each read at one version (D.2 r3): the group's preferred version when it
// serves the resource there, otherwise the first version in version order that does.
func (c *call) otherKinds(groups []group) []listedKind {
	var out []listedKind
	for _, g := range groups {
		served := map[string][]string{} // resource -> versions
		kinds := map[string]string{}    // "<resource>@<version>" -> kind
		for _, v := range g.versions {
			d := c.docs[g.name+"/"+v]
			if d == nil || d.list == nil {
				continue
			}
			for _, res := range d.list.APIResources {
				if !res.Namespaced || strings.Contains(res.Name, "/") || !hasVerb(res.Verbs, "list") {
					continue
				}
				served[res.Name] = append(served[res.Name], v)
				kinds[res.Name+"@"+v] = res.Kind
			}
		}
		var chosen []listedKind
		for name, versions := range served {
			v := versions[0]
			for _, sv := range versions {
				if sv == g.preferred {
					v = sv
				}
			}
			chosen = append(chosen, listedKind{readonly.Resource{Group: g.name, Version: v, Resource: name}, kinds[name+"@"+v]})
		}
		sort.Slice(chosen, func(i, j int) bool {
			if chosen[i].kind != chosen[j].kind {
				return chosen[i].kind < chosen[j].kind
			}
			return chosen[i].res.Resource < chosen[j].res.Resource
		})
		out = append(out, chosen...)
	}
	return out
}

// list sends the LIST of one kind in the call's namespace.
func (c *call) list(ctx context.Context, res readonly.Resource) (*unstructured.UnstructuredList, error) {
	r, err := c.reader(ctx)
	if err != nil {
		return nil, err
	}
	return r.List(ctx, res, c.ns)
}

// add keeps one object of the namespace as a node.
func (c *call) add(u *unstructured.Unstructured, k listedKind) *entry {
	uid := string(u.GetUID())
	if e, ok := c.entries[uid]; ok {
		return e
	}
	e := &entry{uid: uid, apiVersion: u.GetAPIVersion(), kind: u.GetKind(), name: u.GetName(), refs: u.GetOwnerReferences()}
	if e.apiVersion == "" {
		e.apiVersion = apiVersion(k.res.Group, k.res.Version)
	}
	if e.kind == "" {
		e.kind = k.kind
	}
	c.entries[uid] = e
	return e
}

// sorted is every node in the order of G6: kind, name, uid (one namespace).
func (c *call) sorted() []*entry {
	out := make([]*entry, 0, len(c.entries))
	for _, e := range c.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		if a.name != b.name {
			return a.name < b.name
		}
		return a.uid < b.uid
	})
	return out
}

// chosen is the reference that decides the place of an object (G1): the one with
// controller true, else the first one.
func chosen(refs []metav1.OwnerReference) (metav1.OwnerReference, bool) {
	for _, r := range refs {
		if r.Controller != nil && *r.Controller {
			return r, true
		}
	}
	if len(refs) == 0 {
		return metav1.OwnerReference{}, false
	}
	return refs[0], true
}

// set gives e a state whose text names its reference.
func (e *entry) set(s State, ref metav1.OwnerReference) {
	e.state, e.ref, e.text, e.placed = s, ref.Kind+"/"+ref.Name, OwnerText(s, ref.Kind, ref.Name), true
}

// place decides the place of e and then of its owner, up the chain (G1 to G5). chain
// holds the uids below e on the chain being walked; an owner already on it is a loop.
func (c *call) place(ctx context.Context, e *entry, chain map[string]bool) error {
	if e.placed {
		return nil
	}
	ref, ok := chosen(e.refs)
	if !ok {
		e.state, e.placed = StateNone, true
		return nil
	}
	s, owner, err := c.resolve(ctx, ref)
	if err != nil {
		return err
	}
	if s != StateContained {
		e.set(s, ref)
		return nil
	}
	uid := string(owner.GetUID())
	if uid == e.uid || chain[uid] {
		e.set(StateLoop, ref)
		return nil
	}
	oe, ok := c.entries[uid]
	if !ok {
		oe = c.add(owner, listedKind{})
	}
	e.state, e.parent, e.placed = StateContained, uid, true
	next := map[string]bool{e.uid: true}
	for k := range chain {
		next[k] = true
	}
	return c.place(ctx, oe, next)
}

// resolve looks up the owner that ref names (G2 to G4). It returns StateContained with
// the owner object for a namespaced owner that passes G2, or the state that the lookup
// gives instead.
func (c *call) resolve(ctx context.Context, ref metav1.OwnerReference) (State, *unstructured.Unstructured, error) {
	group, version := splitAPIVersion(ref.APIVersion)
	d, err := c.document(ctx, group, version)
	if err != nil {
		return "", nil, err
	}
	d.needed = true
	if d.failed {
		if d.status == http.StatusNotFound {
			return StateMissing, nil, nil
		}
		return "", nil, &LoadError{Namespace: c.ns, Reason: d.reason}
	}
	var res *metav1.APIResource
	for i := range d.list.APIResources {
		if r := &d.list.APIResources[i]; r.Kind == ref.Kind && !strings.Contains(r.Name, "/") {
			res = r
			break
		}
	}
	if res == nil {
		return StateMissing, nil, nil
	}
	ns := ""
	if res.Namespaced {
		ns = c.ns
	}
	obj, err := c.object(ctx, readonly.Resource{Group: group, Version: version, Resource: res.Name}, ns, ref.Name)
	if err != nil {
		if code, ok := statusCode(err); ok {
			switch code {
			case http.StatusNotFound:
				return StateMissing, nil, nil
			case http.StatusForbidden:
				return StateForbidden, nil, nil
			}
		}
		return "", nil, c.fail(err)
	}
	// G2, also for an owner read from the cache.
	og, _ := splitAPIVersion(obj.GetAPIVersion())
	if string(obj.GetUID()) != string(ref.UID) || obj.GetKind() != ref.Kind || obj.GetName() != ref.Name || obj.GetNamespace() != ns || og != group {
		return StateMissing, nil, nil
	}
	if !res.Namespaced {
		return StateClusterScoped, nil, nil
	}
	return StateContained, obj, nil
}

// object sends one owner GET at most once per call. An error of the call's own end is
// returned as it is and not kept.
func (c *call) object(ctx context.Context, res readonly.Resource, ns, name string) (*unstructured.Unstructured, error) {
	key := res.Group + "/" + res.Version + "/" + res.Resource + "/" + ns + "/" + name
	if f, ok := c.owners[key]; ok {
		return f.obj, f.err
	}
	r, err := c.reader(ctx)
	if err != nil {
		return nil, err
	}
	obj, err := r.Object(ctx, res, ns, name)
	c.owners[key] = &fetched{obj: obj, err: err}
	return obj, err
}

// get sends one GET of an API path and reads its body.
func get(ctx context.Context, r Reader, path string) (int, []byte, error) {
	resp, err := r.Get(ctx, path)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, documentLimit))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

// statusCode is the HTTP status of an API error, read from its type (never its text).
func statusCode(err error) (int, bool) {
	var s apierrors.APIStatus
	if errors.As(err, &s) && s.Status().Code != 0 {
		return int(s.Status().Code), true
	}
	return 0, false
}

// reasonOf is the status number of an API error, or NoAnswer.
func reasonOf(err error) string {
	if code, ok := statusCode(err); ok {
		return strconv.Itoa(code)
	}
	return NoAnswer
}

func hasVerb(verbs []string, v string) bool {
	for _, x := range verbs {
		if x == v {
			return true
		}
	}
	return false
}

var kubeVersion = regexp.MustCompile(`^v([0-9]+)(?:(alpha|beta)([0-9]+))?$`)

// versionBefore orders versions as Kubernetes does: GA before beta before alpha, then the
// higher number first; anything else after them, by name.
func versionBefore(a, b string) bool {
	ma, mb := kubeVersion.FindStringSubmatch(a), kubeVersion.FindStringSubmatch(b)
	switch {
	case ma == nil && mb == nil:
		return a < b
	case ma == nil:
		return false
	case mb == nil:
		return true
	}
	rank := map[string]int{"": 0, "beta": 1, "alpha": 2}
	if rank[ma[2]] != rank[mb[2]] {
		return rank[ma[2]] < rank[mb[2]]
	}
	na, _ := strconv.Atoi(ma[1])
	nb, _ := strconv.Atoi(mb[1])
	if na != nb {
		return na > nb
	}
	sa, _ := strconv.Atoi(ma[3])
	sb, _ := strconv.Atoi(mb[3])
	return sa > sb
}
