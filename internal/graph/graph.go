// Package graph builds the containment answer of one namespace (packet FA2-T5): every
// Pod, ReplicaSet, Deployment, StatefulSet, DaemonSet, Job and CronJob of the namespace,
// every namespaced custom resource found through discovery, and for each one the object
// it sits inside, from its ownerReferences, or why its owner is not used. It reads the
// cluster only through internal/kube/readonly, with one fresh client per cluster
// operation (CLIENT-PER-REQ-T5).
package graph

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

// Reader is the part of the read-only client that one cluster operation uses.
// *readonly.Client implements it.
type Reader interface {
	Get(ctx context.Context, path string) (*http.Response, error)
	List(ctx context.Context, r readonly.Resource, namespace string) (*unstructured.UnstructuredList, error)
	Object(ctx context.Context, r readonly.Resource, namespace, name string) (*unstructured.Unstructured, error)
	ListMetadata(ctx context.Context, r readonly.Resource, namespace string) ([]readonly.ObjectMeta, error)
}

// Open gives one cluster operation its own client. It returns ErrStopped when the call
// must not send another request: the cluster changed, or its state is no longer
// connected. The engine calls it once before every operation and uses the client for
// that operation only.
type Open func(ctx context.Context) (Reader, error)

// ErrStopped is the error of an Open that ends the call (CLIENT-PER-REQ-T5 point 3).
var ErrStopped = errors.New("graph: the call was stopped")

// State is the owner state of one node (packet section 3.1).
type State string

const (
	StateNone          State = "none"
	StateContained     State = "contained"
	StateMissing       State = "owner-missing"
	StateForbidden     State = "owner-forbidden"
	StateClusterScoped State = "owner-cluster-scoped"
	StateLoop          State = "owner-loop"
)

// Answer is the response of GET /api/graph/containment (packet FA2-T5 D.1, FA2-T6 E.1,
// FA2-T7a appendix F).
type Answer struct {
	Namespace      string         `json:"namespace"`
	NamespaceState NamespaceState `json:"namespaceState"`
	Nodes          []Node         `json:"nodes"`
	Refused        []Refusal      `json:"refused"`
	Bridges        []Bridge       `json:"bridges"`
	Unsupported    []Unsupported  `json:"unsupported"`
}

// The bridge kinds (FA2-T6 E.1).
const (
	BridgeIngressService   = "ingress-service"
	BridgeServicePod       = "service-pod"
	BridgePodPVC           = "pod-pvc"
	BridgePVCPV            = "pvc-pv"
	BridgePodConfigMap     = "pod-configmap"
	BridgePodSecret        = "pod-secret"
	BridgeHPATarget        = "hpa-target"
	BridgeHTTPRouteService = "httproute-service"
	BridgePVCStorageClass  = "pvc-storageclass"
	BridgePVStorageClass   = "pv-storageclass"
)

// The states of a bridge (FA2-T7a 3.1), from the most severe to the least.
const (
	BridgeBroken    = "broken"
	BridgeWarning   = "warning"
	BridgeUnknown   = "unknown"
	BridgeWaiting   = "waiting"
	BridgeOptional  = "optional"
	BridgeConnected = "connected"
)

// The states of a namespace answer (FA2-T7a 3.4).
const (
	NamespaceOK           = "ok"
	NamespaceEmpty        = "empty"
	NamespaceNotFound     = "not-found"
	NamespaceNoPermission = "no-permission"
)

// NamespaceState says whether the namespace of the answer holds resources, does not
// exist or cannot be read (FA2-T7a 3.4). ExistenceChecked is true only when the GET of the
// Namespace object answered 200 or 404.
type NamespaceState struct {
	State            string `json:"state"`
	ExistenceChecked bool   `json:"existenceChecked"`
}

// Bridge is one declared reference from a node of the answer to the object it names,
// with the settings that made it (FA2-T6 E.1, section 3.1). State is one of the six bridge
// states and Text, the cause of a state other than connected (FA2-T7a 3.1, 3.2). Optional
// is set only on Pod -> ConfigMap and Pod -> Secret bridges: one flag per settings entry,
// the `optional` of the reference that entry comes from (E.4; never part of the text).
type Bridge struct {
	Kind     string   `json:"kind"`
	From     From     `json:"from"`
	To       Target   `json:"to"`
	State    string   `json:"state"`
	Settings []string `json:"settings"`
	Optional []bool   `json:"optional,omitempty"`
	Text     string   `json:"text,omitempty"`
}

// From is the node a bridge leaves.
type From struct {
	UID string `json:"uid"`
}

// Target is the object a bridge names. Group is "" for the core group and Namespace is
// "" for a cluster-scoped target. UID is the uid of the node of the answer with the same
// group, kind, namespace and name, or null (B5).
type Target struct {
	Group     string  `json:"group"`
	Namespace string  `json:"namespace"`
	Kind      string  `json:"kind"`
	Name      string  `json:"name"`
	UID       *string `json:"uid"`
}

// Unsupported is a resource whose objects are nodes but whose version the bridges are
// not read from (B4, BT-6).
type Unsupported struct {
	Group   string `json:"group"`
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Text    string `json:"text"`
}

// Node is one object of the answer. Parent is the uid of the object it sits inside, or
// null when it sits directly under the namespace.
type Node struct {
	UID        string  `json:"uid"`
	APIVersion string  `json:"apiVersion"`
	Kind       string  `json:"kind"`
	Name       string  `json:"name"`
	Parent     *string `json:"parent"`
	Owner      Owner   `json:"owner"`
	// NodeState is set on every core v1 Pod node and nil on every other node, so the
	// JSON of a node of another kind has no nodeState key (FA2-T7b 6.2).
	NodeState *NodeState `json:"nodeState,omitempty"`
}

// NodeState is the state of a Pod node and its one fixed text (FA2-T7b 3.1); Text is
// empty for healthy.
type NodeState struct {
	State string `json:"state"`
	Text  string `json:"text,omitempty"`
}

// The states of a Pod node (FA2-T7b 3.1, UI.md 4.3).
const (
	NodeHealthy     = "healthy"
	NodeProgressing = "progressing"
	NodeFailing     = "failing"
	NodeUnknown     = "unknown"
)

// Owner is the owner state of a node. Reference is the chosen ownerReference as
// <kind>/<name> and Text its fixed text; both are empty for none and contained.
type Owner struct {
	State     State  `json:"state"`
	Reference string `json:"reference,omitempty"`
	Text      string `json:"text,omitempty"`
}

// Refusal is one part of the namespace that could not be read (GA-4, GA-6): a kind
// whose LIST was refused, or a discovery document of a group version. Status is the
// HTTP status actually received (0 when no answer came); Reason is that number or
// "no answer".
type Refusal struct {
	Kind    string `json:"kind,omitempty"`
	Group   string `json:"group,omitempty"`
	Version string `json:"version,omitempty"`
	Status  int    `json:"status,omitempty"`
	Reason  string `json:"reason"`
	Text    string `json:"text"`
}

// LoadError is a call that failed as a whole (G4, GA-3): an owner lookup or a discovery
// document that an owner decision needs ended with neither 403 nor 404, or a LIST or
// /apis failed. Reason is the HTTP status number or "no answer". Kind is set only by the
// LISTs of the bridge kinds (FA2-T6 B3), so the text names what failed.
type LoadError struct {
	Namespace string
	Reason    string
	Kind      string
}

// Error is the text of the failed call: the kind whose LIST failed when it is known (in
// the namespace, or of the cluster answer when Namespace is empty), else the owners text.
func (e *LoadError) Error() string {
	switch {
	case e.Kind != "" && e.Namespace == "":
		return ClusterLoadText(e.Kind, e.Reason)
	case e.Kind != "":
		return KindLoadText(e.Kind, e.Namespace, e.Reason)
	}
	return LoadText(e.Namespace, e.Reason)
}

// NoAnswer is the reason of a request that got no HTTP answer (P-T5-3).
const NoAnswer = "no answer"

// OwnerText is the fixed English text of an owner state (packet section 3.1, OV-4). It
// is the one place these bytes are made; the screen shows them as they are.
func OwnerText(s State, kind, name string) string {
	switch s {
	case StateMissing:
		return fmt.Sprintf("Owner %s/%s not found", kind, name)
	case StateForbidden:
		return "Owner not readable \u2014 no permission"
	case StateClusterScoped:
		return fmt.Sprintf("Owner %s/%s is cluster-scoped", kind, name)
	case StateLoop:
		return fmt.Sprintf("Owner %s/%s not shown: owner references form a loop", kind, name)
	}
	return ""
}

// KindText is the text of a kind whose LIST was refused (GA-4, UI.md J-6 form).
func KindText(kind string, status int) string {
	return fmt.Sprintf("Couldn't load %s (%d)", Plural(kind), status)
}

// GroupText is the text of a discovery document that could not be read (GA-6).
func GroupText(group, version, reason string) string {
	return fmt.Sprintf("Couldn't load API group %s/%s (%s)", group, version, reason)
}

// LoadText is the text of a call that failed as a whole (GA-3, P-T5-3).
func LoadText(namespace, reason string) string {
	return fmt.Sprintf("Couldn't load owners in %s (%s)", namespace, reason)
}

// KindLoadText is the text of a call that failed as a whole on the LIST of a bridge kind
// (FA2-T6 C11).
func KindLoadText(kind, namespace, reason string) string {
	return fmt.Sprintf("Couldn't load %s in %s (%s)", Plural(kind), namespace, reason)
}

// The texts of an HTTPRoute bridge and of an unsupported version (FA2-T6 section 3.2).
const ServicesForbiddenText = "no permission to read Services"

// ServiceNotFoundText is BT-3: the Services LIST succeeded and the target is not there.
func ServiceNotFoundText(name string) string { return fmt.Sprintf("Service %s not found", name) }

// OtherNamespaceText is BT-5: the target is in another namespace and is not read.
func OtherNamespaceText(namespace, name string) string {
	return fmt.Sprintf("Service %s/%s is in another namespace (not checked)", namespace, name)
}

// UnsupportedText is BT-6.
func UnsupportedText(version string) string { return "Unsupported version " + version }

// ClusterLoadText is the text of a cluster answer that failed on the LIST of kind
// (FA2-T7a 6.5).
func ClusterLoadText(kind, reason string) string {
	return fmt.Sprintf("Couldn't load %s (%s)", Plural(kind), reason)
}

// The texts of the bridge states (FA2-T7a 3.2): the cause part of UI.md 4.4, without the
// state word.

// NotFoundText is I-2, C-2, C-3, P-2, V-2 and H-2: the target's LIST succeeded and it is
// not there.
func NotFoundText(kind, name string) string { return fmt.Sprintf("%s %s not found", kind, name) }

// NoPermissionText is S-5, C-4, P-4, V-8, K-4 and H-3: the target's LIST was refused.
func NoPermissionText(kind string) string { return "no permission to read " + Plural(kind) }

// SelectorNoPodsText is S-2; selector is the pairs as the selector setting prints them.
func SelectorNoPodsText(selector string) string {
	return fmt.Sprintf("selector %s matches 0 Pods", selector)
}

// NamedPortText is S-3.
func NamedPortText(name string) string {
	return fmt.Sprintf("targetPort %q: no container port with this name", name)
}

// NumericPortText is S-4.
func NumericPortText(port int) string {
	return fmt.Sprintf("targetPort %d is not declared by the container (traffic not verifiable)", port)
}

// ServicePortText is I-3 and ServicePortNameText I-4.
func ServicePortText(service string, port int) string {
	return fmt.Sprintf("Service %s has no port %d", service, port)
}

func ServicePortNameText(service, port string) string {
	return fmt.Sprintf("Service %s has no port named %q", service, port)
}

// EphemeralWaitText is P-3.
func EphemeralWaitText(claim, pod string) string {
	return fmt.Sprintf("%s waits to be created for Pod %s", claim, pod)
}

// The waiting texts of a claim without a volume: V-3, V-4, V-5 and V-6.
func ConsumerPodWaitText(claim, pod string) string {
	return fmt.Sprintf("%s waits for consumer Pod %s", claim, pod)
}

func ConsumerWaitText(claim string) string { return claim + " waits for a consumer Pod" }

func ProvisionWaitText(claim string) string { return claim + " waits for a volume to be provisioned" }

func VolumeWaitText(claim string) string { return claim + " waits for a matching PersistentVolume" }

// ClassUnboundText is K-2 and V-7, ClassBoundText K-3.
func ClassUnboundText(class string) string {
	return fmt.Sprintf("StorageClass %q not found (claim cannot bind)", class)
}

func ClassBoundText(class string) string {
	return fmt.Sprintf("StorageClass %q not found (volume already bound)", class)
}

// NotServedText is H-3 for a group the cluster does not serve; NotReadText is H-4.
func NotServedText(group, kind string) string {
	return fmt.Sprintf("%s %s is not served by this cluster", group, kind)
}

func NotReadText(kind, name string) string {
	return fmt.Sprintf("%s %s is not read by intuitik", kind, name)
}

// Plural is the plural of a kind name for the texts: ReplicaSet -> ReplicaSets,
// Policy -> Policies, Ingress -> Ingresses.
func Plural(kind string) string {
	lower := strings.ToLower(kind)
	switch {
	case strings.HasSuffix(lower, "s"), strings.HasSuffix(lower, "x"), strings.HasSuffix(lower, "ch"), strings.HasSuffix(lower, "sh"):
		return kind + "es"
	case len(lower) > 1 && strings.HasSuffix(lower, "y") && !strings.ContainsRune("aeiou", rune(lower[len(lower)-2])):
		return kind[:len(kind)-1] + "ies"
	}
	return kind + "s"
}

// splitAPIVersion splits an apiVersion into its group ("" for the core group) and version.
func splitAPIVersion(av string) (group, version string) {
	if g, v, ok := strings.Cut(av, "/"); ok {
		return g, v
	}
	return "", av
}

// apiVersion joins a group and a version into an apiVersion.
func apiVersion(group, version string) string {
	if group == "" {
		return version
	}
	return group + "/" + version
}

var namespaceLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// ValidNamespace reports a DNS-1123 label (packet D.1, GA-7).
func ValidNamespace(ns string) bool {
	return len(ns) <= 63 && namespaceLabel.MatchString(ns)
}
