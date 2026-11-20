package graph

import (
	"encoding/json"
	"strings"
	"testing"
)

// dot is U+00B7 with one space on each side, written here independently of the product
// (FA2-T7b section 3).
var dot = " " + string(rune(0x00b7)) + " "

// podCase is one Pod object of C1 and its expected node state (FA2-T7b 3.1).
type podCase struct {
	id, input string
	js        string // the Pod's spec and status as JSON
	state     string
	text      string
}

// podCases are the C1 inputs: N-1 to N-11 in the order of 3.1, with the counterexamples
// of 3.5 and 6.3.
var podCases = []podCase{
	{"N-1", "crashloop-1-restart",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":false,"restartCount":1,"state":{"waiting":{"reason":"CrashLoopBackOff","message":"back-off 10s"}}}]}}`,
		"failing", "CrashLoopBackOff" + dot + "1 restart"},
	{"N-1", "crashloop-7-restarts",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":false,"restartCount":7,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}}`,
		"failing", "CrashLoopBackOff" + dot + "7 restarts"},
	{"N-1", "init-crashloop-7-ahead-of-regular-imagepull-0",
		`{"spec":{"initContainers":[{"name":"setup","image":"busybox"}],"containers":[{"name":"app","image":"registry.example/app:1"}]},"status":{"phase":"Pending","initContainerStatuses":[{"name":"setup","ready":false,"restartCount":7,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}],"containerStatuses":[{"name":"app","ready":false,"restartCount":0,"state":{"waiting":{"reason":"ImagePullBackOff"}}}]}}`,
		"failing", "CrashLoopBackOff" + dot + "7 restarts"},
	{"N-1", "second-container-crashloop-3-after-imagepull",
		`{"spec":{"containers":[{"name":"a","image":"registry.example/a:1"},{"name":"b","image":"busybox"}]},"status":{"phase":"Running","containerStatuses":[{"name":"a","ready":false,"restartCount":0,"state":{"waiting":{"reason":"ImagePullBackOff"}}},{"name":"b","ready":false,"restartCount":3,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}}`,
		"failing", "CrashLoopBackOff" + dot + "3 restarts"},
	{"N-1", "terminated-error-rc-2-ahead-of-crashloop-3",
		`{"spec":{"containers":[{"name":"a","image":"busybox"},{"name":"b","image":"busybox"}]},"status":{"phase":"Running","containerStatuses":[{"name":"a","ready":false,"restartCount":2,"state":{"terminated":{"reason":"Error","exitCode":1}}},{"name":"b","ready":false,"restartCount":3,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}}`,
		"failing", "CrashLoopBackOff" + dot + "3 restarts"},
	{"N-1b", "terminated-error-exit-1-rc-4",
		`{"spec":{"containers":[{"name":"crash","image":"busybox"}]},"status":{"phase":"Running","containerStatuses":[{"name":"crash","ready":false,"restartCount":4,"state":{"terminated":{"reason":"Error","exitCode":1,"message":"CANARY_TERM"}},"lastState":{"terminated":{"reason":"Error","exitCode":1}}}]}}`,
		"failing", "Error" + dot + "4 restarts"},
	{"N-1b", "init-terminated-error-exit-2-rc-1",
		`{"spec":{"initContainers":[{"name":"setup","image":"busybox"}],"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Pending","initContainerStatuses":[{"name":"setup","ready":false,"restartCount":1,"state":{"terminated":{"reason":"Error","exitCode":2}}}],"containerStatuses":[{"name":"app","ready":false,"restartCount":0,"state":{"waiting":{"reason":"PodInitializing"}}}]}}`,
		"failing", "Error" + dot + "1 restart"},
	{"N-1b", "terminated-exit-137-empty-reason-rc-2",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":false,"restartCount":2,"state":{"terminated":{"reason":"","exitCode":137}}}]}}`,
		"failing", "exit 137" + dot + "2 restarts"},
	{"N-9", "not-n1b-terminated-completed-exit-0-rc-3-always",
		`{"spec":{"restartPolicy":"Always","containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":false,"restartCount":3,"state":{"terminated":{"reason":"Completed","exitCode":0}}}]}}`,
		"progressing", "Running" + dot + "0/1 ready"},
	{"N-9", "not-n1b-terminated-exit-0-rc-2-onfailure",
		`{"spec":{"restartPolicy":"OnFailure","containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":false,"restartCount":2,"state":{"terminated":{"reason":"Completed","exitCode":0}}}]}}`,
		"progressing", "Running" + dot + "0/1 ready"},
	{"N-9", "not-n1b-terminated-error-exit-1-rc-0",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":false,"restartCount":0,"state":{"terminated":{"reason":"Error","exitCode":1}}}]}}`,
		"progressing", "Running" + dot + "0/1 ready"},
	{"N-10", "not-n1b-init-terminated-exit-0-rc-2-running-pod",
		`{"spec":{"initContainers":[{"name":"setup","image":"busybox"}],"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Running","initContainerStatuses":[{"name":"setup","ready":true,"restartCount":2,"state":{"terminated":{"reason":"Completed","exitCode":0}}}],"containerStatuses":[{"name":"app","ready":true,"restartCount":0,"state":{"running":{}}}]}}`,
		"healthy", ""},
	{"N-10", "not-n1b-recovered-running-ready-rc-5-laststate-error",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":true,"restartCount":5,"state":{"running":{}},"lastState":{"terminated":{"reason":"Error","exitCode":1}}}]}}`,
		"healthy", ""},
	{"N-5", "not-n1b-phase-failed-terminated-error-rc-1",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Failed","containerStatuses":[{"name":"app","ready":false,"restartCount":1,"state":{"terminated":{"reason":"Error","exitCode":1}}}]}}`,
		"failing", "Failed"},
	{"N-2", "imagepullbackoff",
		`{"spec":{"containers":[{"name":"app","image":"registry.k8s.io/pause:intuitik-a2-missing"}]},"status":{"phase":"Pending","containerStatuses":[{"name":"app","ready":false,"restartCount":0,"image":"registry.k8s.io/pause:other","state":{"waiting":{"reason":"ImagePullBackOff","message":"Back-off pulling image"}}}]}}`,
		"failing", "ImagePullBackOff" + dot + "registry.k8s.io/pause:intuitik-a2-missing"},
	{"N-2", "errimagepull",
		`{"spec":{"containers":[{"name":"app","image":"registry.k8s.io/pause:intuitik-a2-missing"}]},"status":{"phase":"Pending","containerStatuses":[{"name":"app","ready":false,"restartCount":0,"state":{"waiting":{"reason":"ErrImagePull"}}}]}}`,
		"failing", "ErrImagePull" + dot + "registry.k8s.io/pause:intuitik-a2-missing"},
	{"N-2", "imagepull-status-not-in-spec",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Pending","containerStatuses":[{"name":"gone","ready":false,"restartCount":0,"state":{"waiting":{"reason":"ErrImagePull"}}}]}}`,
		"failing", "ErrImagePull"},
	{"N-3", "createcontainerconfigerror",
		`{"spec":{"containers":[{"name":"app","image":"busybox","envFrom":[{"configMapRef":{"name":"app-config"}}]}]},"status":{"phase":"Pending","containerStatuses":[{"name":"app","ready":false,"restartCount":0,"state":{"waiting":{"reason":"CreateContainerConfigError","message":"configmap \"app-config\" not found"}}}]}}`,
		"failing", "CreateContainerConfigError"},
	{"N-4", "invalidimagename",
		`{"spec":{"containers":[{"name":"app","image":"BAD"}]},"status":{"phase":"Pending","containerStatuses":[{"name":"app","ready":false,"restartCount":0,"state":{"waiting":{"reason":"InvalidImageName"}}}]}}`,
		"failing", "InvalidImageName"},
	{"N-4", "createcontainererror",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Pending","containerStatuses":[{"name":"app","ready":false,"restartCount":0,"state":{"waiting":{"reason":"CreateContainerError"}}}]}}`,
		"failing", "CreateContainerError"},
	{"N-4", "runcontainererror",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":false,"restartCount":2,"state":{"waiting":{"reason":"RunContainerError"}}}]}}`,
		"failing", "RunContainerError"},
	{"N-5", "failed",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Failed","message":"exit 1"}}`,
		"failing", "Failed"},
	{"N-5", "failed-evicted",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Failed","reason":"Evicted","message":"The node was low on resource: memory."}}`,
		"failing", "Failed" + dot + "Evicted"},
	{"N-6", "pending-unscheduled",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"False","reason":"Unschedulable","message":"0/1 nodes are available"}]}}`,
		"progressing", "Pending" + dot + "scheduling"},
	{"N-7", "pending-containercreating",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"True"}],"containerStatuses":[{"name":"app","ready":false,"restartCount":0,"state":{"waiting":{"reason":"ContainerCreating"}}}]}}`,
		"progressing", "Pending" + dot + "ContainerCreating"},
	{"N-7", "pending-podinitializing",
		`{"spec":{"initContainers":[{"name":"setup","image":"busybox"}],"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Pending","initContainerStatuses":[{"name":"setup","ready":false,"restartCount":0,"state":{"running":{}}}],"containerStatuses":[{"name":"app","ready":false,"restartCount":0,"state":{"waiting":{"reason":"PodInitializing"}}}]}}`,
		"progressing", "Pending" + dot + "PodInitializing"},
	{"N-8", "pending",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Pending"}}`,
		"progressing", "Pending"},
	{"N-9", "running-1-of-2-ready",
		`{"spec":{"containers":[{"name":"a","image":"busybox"},{"name":"b","image":"busybox"}]},"status":{"phase":"Running","containerStatuses":[{"name":"a","ready":true,"restartCount":0,"state":{"running":{}}},{"name":"b","ready":false,"restartCount":0,"state":{"running":{}}}]}}`,
		"progressing", "Running" + dot + "1/2 ready"},
	{"N-10", "running-ready",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":true,"restartCount":0,"state":{"running":{}}}]}}`,
		"healthy", ""},
	{"N-10", "running-ready-restartcount-4",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":true,"restartCount":4,"state":{"running":{}},"lastState":{"terminated":{"reason":"Error","exitCode":1}}}]}}`,
		"healthy", ""},
	{"N-10", "succeeded",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Succeeded","containerStatuses":[{"name":"app","ready":false,"restartCount":0,"state":{"terminated":{"reason":"Completed","exitCode":0}}}]}}`,
		"healthy", ""},
	{"N-11", "phase-unknown",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Unknown"}}`,
		"unknown", "Status unknown"},
	{"N-11", "phase-missing",
		`{"spec":{"containers":[{"name":"app","image":"busybox"}]}}`,
		"unknown", "Status unknown"},
	{"N-11", "wrongly-typed-fields",
		`{"spec":{"containers":"app"},"status":{"phase":7,"containerStatuses":"app","conditions":[1]}}`,
		"unknown", "Status unknown"},
}

// TestPodState_Rules (FA2-T7b C1): the node state and text of each 3.1 case, byte for byte,
// read from the answer of Build.
func TestPodState_Rules(t *testing.T) {
	for i, c := range podCases {
		f := newFake()
		uid := "pod-" + c.input
		f.add(objJSON("v1", "Pod", c.input, uid, c.js))
		a := mustBuild(t, f)
		actual := "absent"
		for _, n := range a.Nodes {
			if n.UID != uid {
				continue
			}
			actual = "nil"
			if n.NodeState != nil {
				actual = n.NodeState.State + " text=" + printableDot(n.NodeState.Text)
			}
		}
		expected := c.state + " text=" + printableDot(c.text)
		t.Logf("FA2-T7b C1 %s input=%s expected=%s actual=%s result=%s", c.id, c.input, expected, actual, passFail(actual == expected))
		if actual != expected {
			t.Errorf("C1 case %d %s %s: got %s, want %s", i, c.id, c.input, actual, expected)
		}
	}
}

// TestPodState_JSON (FA2-T7b C1, 6.2): a core Pod node carries nodeState, with no text key
// when healthy; a node of another group whose kind is Pod and every non-Pod node have no
// nodeState key; no message, env or command value reaches the answer.
func TestPodState_JSON(t *testing.T) {
	f := newFake()
	f.crd("example.com", "v1", "Pod", "xpods", "v1")
	f.add(
		obj("apps/v1", "Deployment", "web", "d-web"),
		obj("apps/v1", "ReplicaSet", "web-1", "rs-web", ctl("apps/v1", "Deployment", "web", "d-web")),
		objJSON("v1", "Pod", "web-1-a", "p-ready",
			`{"spec":{"containers":[{"name":"app","image":"busybox","command":["sh","-c","CANARY_CMD"],"env":[{"name":"K","value":"CANARY_ENV"}]}]},"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":true,"restartCount":0,"state":{"running":{}}}]}}`,
			ctl("apps/v1", "ReplicaSet", "web-1", "rs-web")),
		objJSON("v1", "Pod", "web-1-b", "p-config",
			`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Pending","message":"CANARY_MSG","containerStatuses":[{"name":"app","ready":false,"restartCount":0,"state":{"waiting":{"reason":"CreateContainerConfigError","message":"CANARY_WAIT"}}}]}}`,
			ctl("apps/v1", "ReplicaSet", "web-1", "rs-web")),
		objJSON("v1", "Pod", "web-1-c", "p-exit",
			`{"spec":{"containers":[{"name":"app","image":"busybox"}]},"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":false,"restartCount":1,"state":{"terminated":{"reason":"Error","exitCode":1,"message":"CANARY_TERM"}},"lastState":{"terminated":{"reason":"OOMKilled","exitCode":137,"message":"CANARY_LAST"}}}]}}`,
			ctl("apps/v1", "ReplicaSet", "web-1", "rs-web")),
		objJSON("example.com/v1", "Pod", "custom", "x-pod",
			`{"status":{"phase":"Failed"}}`),
	)
	a := mustBuild(t, f)
	want := map[string]string{
		"p-ready":  `"nodeState":{"state":"healthy"}}`,
		"p-config": `"nodeState":{"state":"failing","text":"CreateContainerConfigError"}}`,
		"p-exit":   `"nodeState":{"state":"failing","text":"Error` + dot + `1 restart"}}`,
		"x-pod":    "no nodeState key",
		"d-web":    "no nodeState key",
		"rs-web":   "no nodeState key",
	}
	seen := 0
	for _, n := range a.Nodes {
		w, ok := want[n.UID]
		if !ok {
			continue
		}
		seen++
		raw, _ := json.Marshal(n)
		s := string(raw)
		actual := "no nodeState key"
		if i := strings.Index(s, `"nodeState"`); i >= 0 {
			actual = s[i:]
		}
		t.Logf("FA2-T7b C1 json input=%s/%s expected=%s actual=%s result=%s", n.Kind, n.Name, printableDot(w), printableDot(actual), passFail(actual == w))
		if actual != w {
			t.Errorf("C1 json %s/%s (%s): got %s, want %s", n.Kind, n.Name, n.APIVersion, actual, w)
		}
	}
	if seen != len(want) {
		t.Errorf("C1 json: %d of %d nodes in the answer", seen, len(want))
	}
	raw, _ := json.Marshal(a)
	for _, canary := range []string{"CANARY_CMD", "CANARY_ENV", "CANARY_MSG", "CANARY_WAIT", "CANARY_TERM", "CANARY_LAST"} {
		n := strings.Count(string(raw), canary)
		t.Logf("FA2-T7b C1 canary input=%s expected=0 actual=%d result=%s", canary, n, passFail(n == 0))
		if n != 0 {
			t.Errorf("C1 canary %s found %d times in the answer", canary, n)
		}
	}
}

// printableDot writes U+00B7 as its escape, so the log stays ASCII.
func printableDot(s string) string {
	return strings.ReplaceAll(s, string(rune(0x00b7)), "\\"+"u00b7")
}
