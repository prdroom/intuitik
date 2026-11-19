package graph

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Unit tests of the containment engine (packet FA2-T5 sections 3.3 and 6.2, job unit).
// Each checked object prints one line:
//   FA2-T5 <criterion> input=<fixture> expected=<kind/name parent state> actual=<same> result=<PASS|FAIL>
// and text criteria add expected-text and actual-text, with U+2014 printed as \u2014.

// contextLimit bounds one Build of a unit test; a loop that hangs fails on it.
const contextLimit = 5 * time.Second

// want is the expected place of one object. parent is "<kind>/<name>" or "-".
type want struct {
	kind, name string
	parent     string
	state      State
	text       string
}

// printable writes U+2014 as its escape, so the log stays ASCII.
func printable(s string) string { return strings.ReplaceAll(s, "\u2014", `\u2014`) }

// place is the actual place of the object kind/name in a: "absent" when it is not a node.
func place(a Answer, kind, name string) (parent string, state State, text string, ok bool) {
	byUID := map[string]Node{}
	for _, n := range a.Nodes {
		byUID[n.UID] = n
	}
	for _, n := range a.Nodes {
		if n.Kind != kind || n.Name != name {
			continue
		}
		parent = "-"
		if n.Parent != nil {
			if p, found := byUID[*n.Parent]; found {
				parent = p.Kind + "/" + p.Name
			} else {
				parent = "uid:" + *n.Parent
			}
		}
		return parent, n.Owner.State, n.Owner.Text, true
	}
	return "", "", "", false
}

// check compares every expected object with the answer and prints its line.
func check(t *testing.T, criterion, input string, a Answer, ws ...want) {
	t.Helper()
	for _, w := range ws {
		parent, state, text, ok := place(a, w.kind, w.name)
		expected := fmt.Sprintf("%s/%s %s %s", w.kind, w.name, w.parent, w.state)
		actual := "absent"
		if ok {
			actual = fmt.Sprintf("%s/%s %s %s", w.kind, w.name, parent, state)
		}
		pass := ok && parent == w.parent && state == w.state && text == w.text
		line := fmt.Sprintf("FA2-T5 %s input=%s expected=%s actual=%s", criterion, input, expected, actual)
		if w.text != "" || text != "" {
			line += fmt.Sprintf(" expected-text=%s actual-text=%s", printable(w.text), printable(text))
		}
		result := "PASS"
		if !pass {
			result = "FAIL"
		}
		t.Logf("%s result=%s", line, result)
		if !pass {
			t.Errorf("FA2-T5 %s %s/%s: got %s text %q, want %s text %q", criterion, w.kind, w.name, actual, text, expected, w.text)
		}
	}
}

// mustBuild builds the answer and fails the test on an error.
func mustBuild(t *testing.T, f *fakeAPI) Answer {
	t.Helper()
	a, err := f.build(t)
	if err != nil {
		t.Fatalf("Build: %v (requests %v)", err, f.requests)
	}
	return a
}

// TestOwnerText (OV-4, section 3.1): the texts of one formatting function, byte for byte.
func TestOwnerText(t *testing.T) {
	cases := []struct{ name, got, want string }{
		{"owner-missing", OwnerText(StateMissing, "Deployment", "gone"), "Owner Deployment/gone not found"},
		{"owner-forbidden", OwnerText(StateForbidden, "ReplicaSet", "web"), "Owner not readable \u2014 no permission"},
		{"owner-cluster-scoped", OwnerText(StateClusterScoped, "Node", "kind-control-plane"), "Owner Node/kind-control-plane is cluster-scoped"},
		{"owner-loop", OwnerText(StateLoop, "ReplicaSet", "a"), "Owner ReplicaSet/a not shown: owner references form a loop"},
		{"none", OwnerText(StateNone, "Pod", "x"), ""},
		{"contained", OwnerText(StateContained, "Pod", "x"), ""},
		{"GA-4", KindText("ReplicaSet", 403), "Couldn't load ReplicaSets (403)"},
		{"GA-4 custom", KindText("Widget", 403), "Couldn't load Widgets (403)"},
		{"GA-6", GroupText("example.com", "v1", "503"), "Couldn't load API group example.com/v1 (503)"},
		{"GA-6 no answer", GroupText("example.com", "v1", NoAnswer), "Couldn't load API group example.com/v1 (no answer)"},
		{"GA-3", LoadText("team-a", "500"), "Couldn't load owners in team-a (500)"},
		{"GA-3 no answer", LoadText("team-a", NoAnswer), "Couldn't load owners in team-a (no answer)"},
	}
	for _, c := range cases {
		pass := c.got == c.want
		t.Logf("FA2-T5 OV-4 input=%s expected-text=%s actual-text=%s result=%v", c.name, printable(c.want), printable(c.got), map[bool]string{true: "PASS", false: "FAIL"}[pass])
		if !pass {
			t.Errorf("%s: %q, want %q", c.name, c.got, c.want)
		}
	}
	forbidden := OwnerText(StateForbidden, "", "")
	r, _ := utf8.DecodeRuneInString(forbidden[len("Owner not readable "):])
	if r != '\u2014' {
		t.Errorf("owner-forbidden: the character after \"readable \" is %U, want U+2014", r)
	}
}

func deploymentFixture() *fakeAPI {
	return newFake().add(
		obj("apps/v1", "Deployment", "web", "d-web"),
		obj("apps/v1", "ReplicaSet", "web-1", "rs-1", ctl("apps/v1", "Deployment", "web", "d-web")),
		obj("apps/v1", "ReplicaSet", "web-2", "rs-2", ctl("apps/v1", "Deployment", "web", "d-web")),
		obj("v1", "Pod", "web-1-a", "p-1a", ctl("apps/v1", "ReplicaSet", "web-1", "rs-1")),
		obj("v1", "Pod", "web-2-a", "p-2a", ctl("apps/v1", "ReplicaSet", "web-2", "rs-2")),
		obj("v1", "Pod", "web-2-b", "p-2b", ctl("apps/v1", "ReplicaSet", "web-2", "rs-2")),
	)
}

// TestContainment_Deployment (C1): Deployment > ReplicaSet (current and previous
// revision) > Pod.
func TestContainment_Deployment(t *testing.T) {
	a := mustBuild(t, deploymentFixture())
	check(t, "C1", "deployment-two-revisions", a,
		want{"Deployment", "web", "-", StateNone, ""},
		want{"ReplicaSet", "web-1", "Deployment/web", StateContained, ""},
		want{"ReplicaSet", "web-2", "Deployment/web", StateContained, ""},
		want{"Pod", "web-1-a", "ReplicaSet/web-1", StateContained, ""},
		want{"Pod", "web-2-a", "ReplicaSet/web-2", StateContained, ""},
		want{"Pod", "web-2-b", "ReplicaSet/web-2", StateContained, ""})
}

// TestContainment_StatefulSetDaemonSet (C2): StatefulSet > Pod and DaemonSet > Pod; a
// ControllerRevision is never a node.
func TestContainment_StatefulSetDaemonSet(t *testing.T) {
	f := newFake().add(
		obj("apps/v1", "StatefulSet", "db", "s-db"),
		obj("v1", "Pod", "db-0", "p-db0", ctl("apps/v1", "StatefulSet", "db", "s-db")),
		obj("v1", "Pod", "db-1", "p-db1", ctl("apps/v1", "StatefulSet", "db", "s-db")),
		obj("apps/v1", "ControllerRevision", "db-7d9", "cr-db", ctl("apps/v1", "StatefulSet", "db", "s-db")),
		obj("apps/v1", "DaemonSet", "agent", "ds-agent"),
		obj("v1", "Pod", "agent-x1", "p-ag", ctl("apps/v1", "DaemonSet", "agent", "ds-agent")),
	)
	a := mustBuild(t, f)
	check(t, "C2", "statefulset-daemonset", a,
		want{"StatefulSet", "db", "-", StateNone, ""},
		want{"Pod", "db-0", "StatefulSet/db", StateContained, ""},
		want{"Pod", "db-1", "StatefulSet/db", StateContained, ""},
		want{"DaemonSet", "agent", "-", StateNone, ""},
		want{"Pod", "agent-x1", "DaemonSet/agent", StateContained, ""})
	revisions := 0
	for _, n := range a.Nodes {
		if n.Kind == "ControllerRevision" {
			revisions++
		}
	}
	t.Logf("FA2-T5 C2 input=statefulset-daemonset ControllerRevision nodes=%d (want 0) result=%s", revisions, map[bool]string{true: "PASS", false: "FAIL"}[revisions == 0])
	if revisions != 0 {
		t.Errorf("C2: %d ControllerRevision nodes, want 0", revisions)
	}
}

// TestContainment_CronJob (C3): CronJob > Job > Pod.
func TestContainment_CronJob(t *testing.T) {
	f := newFake().add(
		obj("batch/v1", "CronJob", "report", "cj-report"),
		obj("batch/v1", "Job", "report-1", "j-report", ctl("batch/v1", "CronJob", "report", "cj-report")),
		obj("v1", "Pod", "report-1-x", "p-report", ctl("batch/v1", "Job", "report-1", "j-report")),
	)
	check(t, "C3", "cronjob-job", mustBuild(t, f),
		want{"CronJob", "report", "-", StateNone, ""},
		want{"Job", "report-1", "CronJob/report", StateContained, ""},
		want{"Pod", "report-1-x", "Job/report-1", StateContained, ""})
}

// widgetFake serves the test CRD: kind Widget, plural wdgts (not "widgets", G3).
func widgetFake() *fakeAPI {
	f := newFake()
	f.crd("example.com", "v1", "Widget", "wdgts", "v1")
	return f
}

// TestContainment_CustomOwner (C4, P-T5-6): custom resources as owners, as children and
// as nodes found through discovery.
func TestContainment_CustomOwner(t *testing.T) {
	f := widgetFake().add(
		obj("example.com/v1", "Widget", "w1", "w-1"),
		obj("example.com/v1", "Widget", "w2", "w-2"),
		obj("apps/v1", "Deployment", "api", "d-api", ctl("example.com/v1", "Widget", "w1", "w-1")),
		obj("apps/v1", "ReplicaSet", "api-1", "rs-api", ctl("apps/v1", "Deployment", "api", "d-api")),
		obj("v1", "Pod", "api-1-a", "p-api", ctl("apps/v1", "ReplicaSet", "api-1", "rs-api")),
		obj("v1", "Pod", "lone", "p-lone", ctl("example.com/v1", "Widget", "w2", "w-2")),
		obj("example.com/v1", "Widget", "w3", "w-3", ctl("apps/v1", "Deployment", "api", "d-api")),
		obj("example.com/v1", "Widget", "w4", "w-4"),
	)
	a := mustBuild(t, f)
	check(t, "C4", "custom-owner", a,
		want{"Widget", "w1", "-", StateNone, ""},
		want{"Widget", "w2", "-", StateNone, ""},
		want{"Deployment", "api", "Widget/w1", StateContained, ""},
		want{"ReplicaSet", "api-1", "Deployment/api", StateContained, ""},
		want{"Pod", "api-1-a", "ReplicaSet/api-1", StateContained, ""},
		want{"Pod", "lone", "Widget/w2", StateContained, ""},
		want{"Widget", "w3", "Deployment/api", StateContained, ""},
		want{"Widget", "w4", "-", StateNone, ""})
}

// TestContainment_NoOwner (C5): objects without an ownerReference sit under the namespace.
func TestContainment_NoOwner(t *testing.T) {
	f := newFake().add(
		obj("v1", "Pod", "debug", "p-debug"),
		obj("batch/v1", "Job", "once", "j-once"),
		obj("v1", "Pod", "once-x", "p-once", ctl("batch/v1", "Job", "once", "j-once")),
	)
	check(t, "C5", "no-owner", mustBuild(t, f),
		want{"Pod", "debug", "-", StateNone, ""},
		want{"Job", "once", "-", StateNone, ""},
		want{"Pod", "once-x", "Job/once", StateContained, ""})
}

// TestContainment_OwnerMissing (C6, C7, C8, G2, G3): the owner does not exist, exists
// under the same name with another UID, is of a kind discovery does not serve, or its
// served document has no such kind.
func TestContainment_OwnerMissing(t *testing.T) {
	t.Run("gone", func(t *testing.T) {
		f := newFake().add(
			obj("apps/v1", "ReplicaSet", "orphan", "rs-orphan", ctl("apps/v1", "Deployment", "gone", "d-gone")),
			obj("v1", "Pod", "orphan-1", "p-orphan", ctl("apps/v1", "ReplicaSet", "orphan", "rs-orphan")),
		)
		check(t, "C6", "deployment-gone", mustBuild(t, f),
			want{"ReplicaSet", "orphan", "-", StateMissing, "Owner Deployment/gone not found"},
			want{"Pod", "orphan-1", "ReplicaSet/orphan", StateContained, ""})
	})
	t.Run("recreated", func(t *testing.T) {
		f := newFake().add(
			obj("v1", "ConfigMap", "holder", "cm-new"),
			obj("v1", "Pod", "child", "p-child", ctl("v1", "ConfigMap", "holder", "cm-old")),
		)
		check(t, "C7", "owner-recreated", mustBuild(t, f),
			want{"Pod", "child", "-", StateMissing, "Owner ConfigMap/holder not found"})
	})
	t.Run("unserved-kind", func(t *testing.T) {
		f := newFake().add(obj("v1", "Pod", "gadget-child", "p-gc", ctl("unserved.example.com/v1", "Gadget", "g1", "g-1")))
		check(t, "C8", "kind-not-served", mustBuild(t, f),
			want{"Pod", "gadget-child", "-", StateMissing, "Owner Gadget/g1 not found"})
	})
	t.Run("kind-not-in-document", func(t *testing.T) {
		f := widgetFake().add(
			obj("example.com/v1", "Thing", "t1", "t-1"),
			obj("v1", "Pod", "thing-child", "p-tc", ctl("example.com/v1", "Thing", "t1", "t-1")),
		)
		check(t, "C8", "kind-not-in-document", mustBuild(t, f),
			want{"Pod", "thing-child", "-", StateMissing, "Owner Thing/t1 not found"})
	})
}

// TestContainment_OwnerForbidden (C9, C10): a 403 on the owner is "no permission",
// whether the owner exists or not, and never "not found".
func TestContainment_OwnerForbidden(t *testing.T) {
	forbidden := "Owner not readable \u2014 no permission"
	t.Run("owner-exists", func(t *testing.T) {
		f := newFake().add(
			obj("apps/v1", "ReplicaSet", "rs-a", "rs-a"),
			obj("v1", "Pod", "pod-a", "p-a", ctl("apps/v1", "ReplicaSet", "rs-a", "rs-a")),
		)
		f.lists["apps/replicasets"] = fail{status: 403}
		f.gets["ReplicaSet/rs-a"] = fail{status: 403}
		check(t, "C9", "owner-exists-403", mustBuild(t, f), want{"Pod", "pod-a", "-", StateForbidden, forbidden})
	})
	t.Run("owner-does-not-exist", func(t *testing.T) {
		f := newFake().add(obj("v1", "Pod", "pod-b", "p-b", ctl("apps/v1", "ReplicaSet", "rs-ghost", "rs-ghost")))
		f.lists["apps/replicasets"] = fail{status: 403}
		f.gets["ReplicaSet/rs-ghost"] = fail{status: 403}
		check(t, "C9", "owner-missing-403", mustBuild(t, f), want{"Pod", "pod-b", "-", StateForbidden, forbidden})
	})
	t.Run("middle", func(t *testing.T) {
		f := newFake().add(
			obj("apps/v1", "Deployment", "web", "d-web"),
			obj("apps/v1", "ReplicaSet", "web-1", "rs-1", ctl("apps/v1", "Deployment", "web", "d-web")),
			obj("v1", "Pod", "web-1-a", "p-1a", ctl("apps/v1", "ReplicaSet", "web-1", "rs-1")),
			obj("v1", "Pod", "web-1-b", "p-1b", ctl("apps/v1", "ReplicaSet", "web-1", "rs-1")),
		)
		f.lists["apps/deployments"] = fail{status: 403}
		f.gets["Deployment/web"] = fail{status: 403}
		check(t, "C10", "deployment-403", mustBuild(t, f),
			want{"ReplicaSet", "web-1", "-", StateForbidden, forbidden},
			want{"Pod", "web-1-a", "ReplicaSet/web-1", StateContained, ""},
			want{"Pod", "web-1-b", "ReplicaSet/web-1", StateContained, ""})
	})
}

// TestContainment_LookupError (C11, G4): an owner lookup that ends with neither 403 nor
// 404 fails the whole call, with no node and never owner-missing or owner-forbidden.
func TestContainment_LookupError(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer fail
		reason string
	}{
		{"500", fail{status: 500}, "500"},
		{"503", fail{status: 503}, "503"},
		{"no answer", fail{noAnswer: true}, NoAnswer},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFake().add(
				obj("apps/v1", "ReplicaSet", "r", "rs-r"),
				obj("v1", "Pod", "p", "p-p", ctl("apps/v1", "ReplicaSet", "r", "rs-r")),
			)
			f.gets["ReplicaSet/r"] = c.answer
			a, err := f.build(t)
			var le *LoadError
			want := LoadText(testNS, c.reason)
			got := ""
			if errors.As(err, &le) {
				got = le.Error()
			}
			pass := got == want && len(a.Nodes) == 0
			t.Logf("FA2-T5 C11 input=owner-get-%s expected=error-no-nodes actual=error=%v nodes=%d expected-text=%s actual-text=%s result=%s",
				c.name, err != nil, len(a.Nodes), want, got, map[bool]string{true: "PASS", false: "FAIL"}[pass])
			if !pass {
				t.Errorf("C11 %s: error %v, %d nodes; want the load error %q and no node", c.name, err, len(a.Nodes), want)
			}
		})
	}
}

// TestContainment_SeveralOwners (C12, G1): the controller reference decides the place,
// else the first reference; the other references never move the object or add a text.
func TestContainment_SeveralOwners(t *testing.T) {
	f := newFake().add(
		obj("apps/v1", "ReplicaSet", "a", "rs-a"),
		obj("apps/v1", "ReplicaSet", "b", "rs-b"),
		obj("v1", "Pod", "with-controller", "p-1",
			ref("apps/v1", "ReplicaSet", "a", "rs-a", false), ref("apps/v1", "ReplicaSet", "b", "rs-b", true)),
		obj("v1", "Pod", "without-controller", "p-2",
			ref("apps/v1", "ReplicaSet", "a", "rs-a", false), ref("apps/v1", "ReplicaSet", "b", "rs-b", false)),
	)
	check(t, "C12", "several-owners", mustBuild(t, f),
		want{"Pod", "with-controller", "ReplicaSet/b", StateContained, ""},
		want{"Pod", "without-controller", "ReplicaSet/a", StateContained, ""})
}

// TestContainment_ClusterScopedOwner (C13, P-T5-2): a Pod owned by a Node.
func TestContainment_ClusterScopedOwner(t *testing.T) {
	node := func() *fakeAPI {
		return newFake().add(
			obj("v1", "Node", "kind-control-plane", "n-1"),
			obj("v1", "Pod", "etcd-kind-control-plane", "p-etcd", ctl("v1", "Node", "kind-control-plane", "n-1")),
		)
	}
	check(t, "C13", "static-pod", mustBuild(t, node()),
		want{"Pod", "etcd-kind-control-plane", "-", StateClusterScoped, "Owner Node/kind-control-plane is cluster-scoped"})
	f := node()
	f.gets["Node/kind-control-plane"] = fail{status: 403}
	check(t, "C13", "static-pod-nodes-403", mustBuild(t, f),
		want{"Pod", "etcd-kind-control-plane", "-", StateForbidden, "Owner not readable \u2014 no permission"})
	if n := len(mustBuild(t, node()).Nodes); n != 1 {
		t.Errorf("C13: %d nodes, want 1 (a Node is never a node of the namespace)", n)
	}
}

// TestContainment_Loop (C14, G5): two objects that own each other. The call returns, no
// object is inside an object that is inside it, and the loop carries owner-loop.
func TestContainment_Loop(t *testing.T) {
	f := newFake().add(
		obj("apps/v1", "ReplicaSet", "a", "rs-a", ctl("apps/v1", "ReplicaSet", "b", "rs-b")),
		obj("apps/v1", "ReplicaSet", "b", "rs-b", ctl("apps/v1", "ReplicaSet", "a", "rs-a")),
	)
	start := time.Now()
	a, err := f.build(t)
	t.Logf("FA2-T5 C14 input=loop returned after %s, error %v", time.Since(start).Round(time.Millisecond), err)
	if err != nil {
		t.Fatalf("C14 loop: %v", err)
	}
	check(t, "C14", "loop", a,
		want{"ReplicaSet", "a", "ReplicaSet/b", StateContained, ""},
		want{"ReplicaSet", "b", "-", StateLoop, "Owner ReplicaSet/a not shown: owner references form a loop"})
	parents := map[string]string{}
	for _, n := range a.Nodes {
		if n.Parent != nil {
			parents[n.UID] = *n.Parent
		}
	}
	for child, parent := range parents {
		if parents[parent] == child {
			t.Errorf("C14: %s is inside %s, which is inside it", child, parent)
		}
	}
}

// chain is a fixture of n ReplicaSets c00 < c01 < ... (each owned by the next) and one
// Pod in c00.
func chain(n int) (*fakeAPI, []want) {
	f := newFake()
	var ws []want
	name := func(i int) string { return fmt.Sprintf("c%02d", i) }
	for i := 0; i < n; i++ {
		if i == n-1 {
			f.add(obj("apps/v1", "ReplicaSet", name(i), "uid-"+name(i)))
			ws = append(ws, want{"ReplicaSet", name(i), "-", StateNone, ""})
			continue
		}
		f.add(obj("apps/v1", "ReplicaSet", name(i), "uid-"+name(i), ctl("apps/v1", "ReplicaSet", name(i+1), "uid-"+name(i+1))))
		ws = append(ws, want{"ReplicaSet", name(i), "ReplicaSet/" + name(i+1), StateContained, ""})
	}
	f.add(obj("v1", "Pod", "bottom", "p-bottom", ctl("apps/v1", "ReplicaSet", name(0), "uid-"+name(0))))
	ws = append(ws, want{"Pod", "bottom", "ReplicaSet/c00", StateContained, ""})
	return f, ws
}

// TestContainment_LongChain (C14, G5): an acyclic chain is contained to its top whatever
// its length: 20 owners (C14) and 40 levels (G5); no depth limit and no state on the way.
func TestContainment_LongChain(t *testing.T) {
	for _, n := range []int{20, 40} {
		f, ws := chain(n)
		check(t, "C14", fmt.Sprintf("chain-%d", n), mustBuild(t, f), ws...)
	}
}

// TestContainment_UIDCache (G5, G2): an owner read once per call is checked against every
// reference that uses it; one UID referenced under two names is accepted under one only.
func TestContainment_UIDCache(t *testing.T) {
	f := newFake().add(
		obj("apps/v1", "ReplicaSet", "real", "rs-real"),
		obj("v1", "Pod", "p1", "p-1", ctl("apps/v1", "ReplicaSet", "real", "rs-real")),
		obj("v1", "Pod", "p2", "p-2", ctl("apps/v1", "ReplicaSet", "real", "rs-real")),
		obj("v1", "Pod", "p3", "p-3", ctl("apps/v1", "ReplicaSet", "alias", "rs-real")),
	)
	check(t, "G5", "uid-under-two-names", mustBuild(t, f),
		want{"Pod", "p1", "ReplicaSet/real", StateContained, ""},
		want{"Pod", "p2", "ReplicaSet/real", StateContained, ""},
		want{"Pod", "p3", "-", StateMissing, "Owner ReplicaSet/alias not found"})
	gets := f.count("GET apps/v1/replicasets " + testNS + "/real")
	t.Logf("FA2-T5 G5 input=uid-under-two-names owner GETs of ReplicaSet/real: %d (want 1)", gets)
	if gets != 1 {
		t.Errorf("G5: %d GETs of ReplicaSet/real in one call, want 1", gets)
	}
}

// TestContainment_SecretOwner (C14b): a core Secret owner is read like any owner; a
// custom kind named Secret is resolved by its own apiVersion; no Secret is listed and no
// Secret value reaches the answer.
func TestContainment_SecretOwner(t *testing.T) {
	canary := "intuitik-canary-secret-value-unit"
	fixture := func() *fakeAPI {
		f := newFake()
		f.crd("vault.example.com", "v1", "Secret", "vaultsecrets", "v1")
		s1 := obj("v1", "Secret", "s1", "sec-1")
		s1.Object["data"] = map[string]any{"value": canary}
		f.add(s1,
			obj("v1", "Pod", "p-s1", "p-s1", ctl("v1", "Secret", "s1", "sec-1")),
			obj("v1", "Pod", "p-gone", "p-gone", ctl("v1", "Secret", "gone", "sec-gone")),
			obj("vault.example.com/v1", "Secret", "vs1", "vsec-1"),
			obj("v1", "Pod", "p-vs1", "p-vs1", ctl("vault.example.com/v1", "Secret", "vs1", "vsec-1")),
		)
		return f
	}
	f := fixture()
	a := mustBuild(t, f)
	check(t, "C14b", "secret-owner", a,
		want{"Pod", "p-s1", "Secret/s1", StateContained, ""},
		want{"Pod", "p-gone", "-", StateMissing, "Owner Secret/gone not found"},
		want{"Pod", "p-vs1", "Secret/vs1", StateContained, ""})
	for _, n := range a.Nodes {
		if n.Name == "vs1" && n.APIVersion != "vault.example.com/v1" {
			t.Errorf("C14b: the custom Secret node has apiVersion %s, want vault.example.com/v1", n.APIVersion)
		}
		if n.Name == "s1" && n.APIVersion != "v1" {
			t.Errorf("C14b: the core Secret node has apiVersion %s, want v1", n.APIVersion)
		}
	}
	body, _ := json.Marshal(a)
	fixtureBody, _ := json.Marshal(f.objects)
	coreLists := f.count("LIST /v1/secrets")
	found, control := strings.Count(string(body), canary), strings.Count(string(fixtureBody), canary)
	t.Logf("FA2-T5 C14b input=secret-owner canary in the answer %d, positive control in the fixture %d, LIST of core secrets %d (want 0, 1, 0)",
		found, control, coreLists)
	if found != 0 || control != 1 || coreLists != 0 {
		t.Errorf("C14b: canary in the answer %d, in the fixture %d, LISTs of secrets %d; want 0, 1, 0", found, control, coreLists)
	}
	f = fixture()
	f.gets["Secret/s1"] = fail{status: 403}
	check(t, "C14b", "secret-owner-get-403", mustBuild(t, f),
		want{"Pod", "p-s1", "-", StateForbidden, "Owner not readable \u2014 no permission"})
}

// TestContainment_ListRefused (C17, GA-4): a refused LIST of a listed kind is reported in
// refused; the Pods stay, and are never owner-missing because their ReplicaSets were not
// listed.
func TestContainment_ListRefused(t *testing.T) {
	f := newFake().add(
		obj("apps/v1", "ReplicaSet", "rs-a", "rs-a"),
		obj("v1", "Pod", "pod-a", "p-a", ctl("apps/v1", "ReplicaSet", "rs-a", "rs-a")),
	)
	f.lists["apps/replicasets"] = fail{status: 403}
	f.gets["ReplicaSet/rs-a"] = fail{status: 403}
	a := mustBuild(t, f)
	check(t, "C17", "replicasets-list-403", a, want{"Pod", "pod-a", "-", StateForbidden, "Owner not readable \u2014 no permission"})
	got := ""
	for _, r := range a.Refused {
		if r.Kind == "ReplicaSet" {
			got = fmt.Sprintf("%s %d %s", r.Kind, r.Status, r.Text)
		}
	}
	want := "ReplicaSet 403 Couldn't load ReplicaSets (403)"
	t.Logf("FA2-T5 C17 input=replicasets-list-403 expected-refused=%s actual-refused=%s result=%s", want, got, map[bool]string{true: "PASS", false: "FAIL"}[got == want])
	if got != want {
		t.Errorf("C17: refused entry %q, want %q (all: %+v)", got, want, a.Refused)
	}
}

// TestContainment_CustomWithoutCRDList (C17b (1), GA-5): custom resources are found
// through discovery, so a user who cannot list CRDs still sees them.
func TestContainment_CustomWithoutCRDList(t *testing.T) {
	f := widgetFake().add(
		obj("example.com/v1", "Widget", "w1", "w-1"),
		obj("v1", "Pod", "in-w1", "p-w1", ctl("example.com/v1", "Widget", "w1", "w-1")),
	)
	f.lists["apiextensions.k8s.io/customresourcedefinitions"] = fail{status: 403}
	a := mustBuild(t, f)
	check(t, "C17b", "no-crd-list", a,
		want{"Widget", "w1", "-", StateNone, ""},
		want{"Pod", "in-w1", "Widget/w1", StateContained, ""})
	t.Logf("FA2-T5 C17b input=no-crd-list refused=%d (want 0)", len(a.Refused))
	if len(a.Refused) != 0 {
		t.Errorf("C17b (1): refused %+v, want none", a.Refused)
	}
}

// TestContainment_GroupDocuments (C17b (2) and (3), GA-6, G3): a failed version document
// that no owner decision needs is reported with the status received and no kind name; one
// that an owner decision needs fails the call unless it is a 404.
func TestContainment_GroupDocuments(t *testing.T) {
	for _, status := range []int{503, 404} {
		t.Run(fmt.Sprintf("unused-%d", status), func(t *testing.T) {
			f := widgetFake().add(obj("v1", "Pod", "plain", "p-plain"))
			f.docs["example.com/v1"] = fail{status: status}
			a := mustBuild(t, f)
			check(t, "C17b", fmt.Sprintf("document-%d", status), a, want{"Pod", "plain", "-", StateNone, ""})
			want := fmt.Sprintf("example.com/v1 %d Couldn't load API group example.com/v1 (%d)", status, status)
			var got []string
			for _, r := range a.Refused {
				got = append(got, strings.TrimSpace(fmt.Sprintf("%s %s/%s %d %s", r.Kind, r.Group, r.Version, r.Status, r.Text)))
			}
			pass := len(got) == 1 && got[0] == want
			t.Logf("FA2-T5 C17b input=document-%d expected-refused=[%s] actual-refused=%q result=%s", status, want, got, map[bool]string{true: "PASS", false: "FAIL"}[pass])
			if !pass {
				t.Errorf("C17b (2): refused %q, want exactly [%s]", got, want)
			}
		})
	}
	t.Run("needed-503", func(t *testing.T) {
		f := widgetFake().add(obj("v1", "Pod", "in-w1", "p-w1", ctl("example.com/v1", "Widget", "w1", "w-1")))
		f.docs["example.com/v1"] = fail{status: 503}
		a, err := f.build(t)
		var le *LoadError
		got := ""
		if errors.As(err, &le) {
			got = le.Error()
		}
		want := LoadText(testNS, "503")
		pass := got == want && len(a.Nodes) == 0
		t.Logf("FA2-T5 C17b input=document-503-needed expected=error-no-nodes actual=nodes=%d expected-text=%s actual-text=%s result=%s",
			len(a.Nodes), want, got, map[bool]string{true: "PASS", false: "FAIL"}[pass])
		if !pass {
			t.Errorf("C17b (3): error %v, %d nodes; want %q and no node", err, len(a.Nodes), want)
		}
	})
}

// TestContainment_VersionPerResource (C17c, D.2 r3): each resource is read at the group's
// preferred version when it is served there, else at the first version that serves it.
func TestContainment_VersionPerResource(t *testing.T) {
	f := newFake()
	f.crd("example.com", "v1", "Widget", "wdgts", "v2")
	f.crd("example.com", "v2", "Widget", "wdgts", "v2")
	f.crd("example.com", "v1", "Gadget", "gadgets", "v2")
	f.add(
		obj("example.com/v2", "Widget", "w", "w-1"),
		obj("example.com/v1", "Gadget", "g", "g-1"),
		obj("v1", "Pod", "in-w", "p-w", ctl("example.com/v2", "Widget", "w", "w-1")),
		obj("v1", "Pod", "in-g", "p-g", ctl("example.com/v1", "Gadget", "g", "g-1")),
	)
	a := mustBuild(t, f)
	check(t, "C17c", "versions", a,
		want{"Widget", "w", "-", StateNone, ""},
		want{"Gadget", "g", "-", StateNone, ""},
		want{"Pod", "in-w", "Widget/w", StateContained, ""},
		want{"Pod", "in-g", "Gadget/g", StateContained, ""})
	versions := map[string]string{}
	for _, n := range a.Nodes {
		versions[n.Kind] = n.APIVersion
	}
	pass := versions["Widget"] == "example.com/v2" && versions["Gadget"] == "example.com/v1"
	t.Logf("FA2-T5 C17c input=versions version per kind Widget=%s Gadget=%s (want example.com/v2, example.com/v1) result=%s",
		versions["Widget"], versions["Gadget"], map[bool]string{true: "PASS", false: "FAIL"}[pass])
	if !pass {
		t.Errorf("C17c: versions %v", versions)
	}
}

// TestContainment_Stable (G6): the same cluster state gives byte-identical JSON, whatever
// the order of the LIST answers.
func TestContainment_Stable(t *testing.T) {
	first := mustBuild(t, deploymentFixture())
	f := deploymentFixture()
	f.reversed = true
	second := mustBuild(t, f)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	t.Logf("FA2-T5 G6 input=deployment-two-revisions answer bytes %d and %d, identical %v", len(a), len(b), string(a) == string(b))
	if string(a) != string(b) {
		t.Errorf("G6: answers differ:\n%s\n%s", a, b)
	}
}

// TestContainment_RequestOrder (D.3, FA2-T6 E.3): the requests of one call form a fixed
// sequence; with no group outside list B it is /apis, the seven LISTs and the four LISTs of
// the bridge kinds (K = 12).
func TestContainment_RequestOrder(t *testing.T) {
	f := newFake()
	mustBuild(t, f)
	want := []string{"GET /apis",
		"LIST /v1/pods " + testNS, "LIST apps/v1/replicasets " + testNS, "LIST apps/v1/deployments " + testNS,
		"LIST apps/v1/statefulsets " + testNS, "LIST apps/v1/daemonsets " + testNS, "LIST batch/v1/jobs " + testNS, "LIST batch/v1/cronjobs " + testNS,
		"LIST /v1/services " + testNS, "LIST /v1/persistentvolumeclaims " + testNS, "LIST networking.k8s.io/v1/ingresses " + testNS,
		"LIST autoscaling/v2/horizontalpodautoscalers " + testNS}
	t.Logf("FA2-T5 D.3 input=empty requests %d %q", len(f.requests), f.requests)
	if fmt.Sprint(f.requests) != fmt.Sprint(want) {
		t.Errorf("D.3: requests %q, want %q", f.requests, want)
	}
}

// TestContainment_ClientPerOperation (CLIENT-PER-REQ-T5 point 2): every operation opens
// its own client and uses it for that one request.
func TestContainment_ClientPerOperation(t *testing.T) {
	f := widgetFake().add(
		obj("example.com/v1", "Widget", "w1", "w-1"),
		obj("apps/v1", "Deployment", "api", "d-api", ctl("example.com/v1", "Widget", "w1", "w-1")),
		obj("apps/v1", "ReplicaSet", "api-1", "rs-api", ctl("apps/v1", "Deployment", "api", "d-api")),
		obj("v1", "Pod", "api-1-a", "p-api", ctl("apps/v1", "ReplicaSet", "api-1", "rs-api")),
	)
	mustBuild(t, f)
	t.Logf("FA2-T5 CLIENT-PER-REQ input=custom-owner opens %d requests %d reused clients %q", f.opens, len(f.requests), f.reused)
	if f.opens != len(f.requests) || len(f.reused) != 0 {
		t.Errorf("CLIENT-PER-REQ: %d opens for %d requests, reused %q; want one client per request", f.opens, len(f.requests), f.reused)
	}
}

// TestContainment_StoppedCall (CLIENT-PER-REQ-T5 point 3): when Open refuses, the call
// ends with ErrStopped: no further request and no partial answer.
func TestContainment_StoppedCall(t *testing.T) {
	f := deploymentFixture()
	f.stopAfter = 4
	a, err := f.build(t)
	t.Logf("FA2-T5 CLIENT-PER-REQ input=stop-at-open-4 error %v, requests %d %q, nodes %d", err, len(f.requests), f.requests, len(a.Nodes))
	if !errors.Is(err, ErrStopped) || len(f.requests) != 3 || len(a.Nodes) != 0 {
		t.Errorf("stopped call: error %v, %d requests, %d nodes; want ErrStopped after 3 requests and no node", err, len(f.requests), len(a.Nodes))
	}
}

// TestContainment_OwnerVersionMismatch (G2, G4; REDSPLIT-T5 amendment A2): the
// reference names example.com/v1; the owner GET is sent at v1 and answers v2 with the same
// uid, kind, name and namespace. The owner is read once, and both children that reference
// it (the second use a cache hit) are owner-missing.
func TestContainment_OwnerVersionMismatch(t *testing.T) {
	f := newFake()
	f.crd("example.com", "v1", "Widget", "wdgts", "v1")
	f.crd("example.com", "v2", "Widget", "wdgts", "v1")
	f.add(
		obj("example.com/v1", "Widget", "w1", "w-1"),
		obj("v1", "Pod", "first", "p-1", ctl("example.com/v1", "Widget", "w1", "w-1")),
		obj("v1", "Pod", "second", "p-2", ctl("example.com/v1", "Widget", "w1", "w-1")),
	)
	f.ownerVersion = map[string]string{"Widget/w1": "example.com/v2"}
	a := mustBuild(t, f)
	gets := f.count("GET example.com/v1/wdgts " + testNS + "/w1")
	t.Logf("FA2-T5 G2 input=owner-version-mismatch requested example.com/v1, returned %s; owner GETs %d (want 1)", f.ownerVersion["Widget/w1"], gets)
	check(t, "G2", "owner-version-mismatch", a,
		want{"Pod", "first", "-", StateMissing, "Owner Widget/w1 not found"},
		want{"Pod", "second", "-", StateMissing, "Owner Widget/w1 not found"})
	if gets != 1 {
		t.Errorf("G2: %d owner GETs of Widget/w1, want 1", gets)
	}
}
