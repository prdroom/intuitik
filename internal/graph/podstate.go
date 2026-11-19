package graph

import (
	"sort"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// textSep joins the parts of a Pod node text: U+00B7 with one space on each side
// (FA2-T7b section 3).
var textSep = " " + string(rune(0x00b7)) + " "

// containerStatus is one container status of a Pod as podState reads it.
type containerStatus struct {
	name     string
	init     bool
	waiting  string // state.waiting.reason, "" when the container is not waiting
	restarts int64
	ready    bool
}

// podState is the node state of a core v1 Pod (FA2-T7b 3.1, rules N-1 to N-11 in order,
// the first match wins). It reads only the phase, the status reason, the conditions, the
// container statuses and the container names and images of the spec; a missing or
// wrongly typed field counts as absent. It never reads env, envFrom, command, args,
// volumes or any message, so no configuration value can reach the text.
func podState(obj *unstructured.Unstructured) NodeState {
	statuses := orderedStatuses(obj)
	for _, s := range statuses {
		if s.waiting == "CrashLoopBackOff" {
			word := " restarts"
			if s.restarts == 1 {
				word = " restart"
			}
			return NodeState{NodeFailing, "CrashLoopBackOff" + textSep + strconv.FormatInt(s.restarts, 10) + word}
		}
	}
	for _, s := range statuses {
		if s.waiting == "ImagePullBackOff" || s.waiting == "ErrImagePull" {
			if image, ok := specImage(obj, s); ok {
				return NodeState{NodeFailing, s.waiting + textSep + image}
			}
			return NodeState{NodeFailing, s.waiting}
		}
	}
	for _, s := range statuses {
		if s.waiting == "CreateContainerConfigError" {
			return NodeState{NodeFailing, s.waiting}
		}
	}
	for _, s := range statuses {
		switch s.waiting {
		case "InvalidImageName", "CreateContainerError", "RunContainerError":
			return NodeState{NodeFailing, s.waiting}
		}
	}
	phase := str(obj.Object, "status", "phase")
	switch phase {
	case "Failed":
		if reason := str(obj.Object, "status", "reason"); reason != "" {
			return NodeState{NodeFailing, "Failed" + textSep + reason}
		}
		return NodeState{NodeFailing, "Failed"}
	case "Pending":
		if unscheduled(obj) {
			return NodeState{NodeProgressing, "Pending" + textSep + "scheduling"}
		}
		for _, s := range statuses {
			if s.waiting != "" {
				return NodeState{NodeProgressing, "Pending" + textSep + s.waiting}
			}
		}
		return NodeState{NodeProgressing, "Pending"}
	case "Running":
		names := specNames(obj, "containers")
		ready := 0
		for _, s := range statuses {
			if !s.init && s.ready && contains(names, s.name) {
				ready++
			}
		}
		if ready < len(names) {
			return NodeState{NodeProgressing, "Running" + textSep + strconv.Itoa(ready) + "/" + strconv.Itoa(len(names)) + " ready"}
		}
		return NodeState{State: NodeHealthy}
	case "Succeeded":
		return NodeState{State: NodeHealthy}
	}
	return NodeState{NodeUnknown, "Status unknown"}
}

// orderedStatuses is the container statuses in the order of 3.1: the init container
// statuses in the order of spec.initContainers, then the container statuses in the order
// of spec.containers, then any status whose name is in neither, in name order.
func orderedStatuses(obj *unstructured.Unstructured) []containerStatus {
	byName := map[string]containerStatus{}
	var rest []containerStatus
	for _, list := range []struct {
		field string
		init  bool
	}{{"initContainerStatuses", true}, {"containerStatuses", false}} {
		items, _, _ := unstructured.NestedFieldNoCopy(obj.Object, "status", list.field)
		values, _ := items.([]any)
		for _, v := range values {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			s := containerStatus{name: str(m, "name"), init: list.init, waiting: str(m, "state", "waiting", "reason")}
			s.restarts = integer(m, "restartCount")
			s.ready, _ = m["ready"].(bool)
			key := s.name
			if list.init {
				key = "init/" + key
			}
			if _, seen := byName[key]; seen {
				rest = append(rest, s)
				continue
			}
			byName[key] = s
		}
	}
	var out []containerStatus
	for _, part := range []struct {
		field, prefix string
	}{{"initContainers", "init/"}, {"containers", ""}} {
		for _, name := range specNames(obj, part.field) {
			if s, ok := byName[part.prefix+name]; ok {
				out = append(out, s)
				delete(byName, part.prefix+name)
			}
		}
	}
	for _, s := range byName {
		rest = append(rest, s)
	}
	sort.SliceStable(rest, func(i, j int) bool {
		if rest[i].name != rest[j].name {
			return rest[i].name < rest[j].name
		}
		return rest[i].init && !rest[j].init
	})
	return append(out, rest...)
}

// specImage is the image of the spec entry with the status's name, as written: the init
// containers first for an init status, the containers first for the others.
func specImage(obj *unstructured.Unstructured, s containerStatus) (string, bool) {
	fields := []string{"containers", "initContainers"}
	if s.init {
		fields = []string{"initContainers", "containers"}
	}
	for _, f := range fields {
		for _, c := range specContainers(obj, f) {
			if str(c, "name") == s.name {
				image, ok := c["image"].(string)
				return image, ok
			}
		}
	}
	return "", false
}

// unscheduled reports a PodScheduled condition with status False.
func unscheduled(obj *unstructured.Unstructured) bool {
	items, _, _ := unstructured.NestedFieldNoCopy(obj.Object, "status", "conditions")
	values, _ := items.([]any)
	for _, v := range values {
		if m, ok := v.(map[string]any); ok && str(m, "type") == "PodScheduled" && str(m, "status") == "False" {
			return true
		}
	}
	return false
}

func specContainers(obj *unstructured.Unstructured, field string) []map[string]any {
	items, _, _ := unstructured.NestedFieldNoCopy(obj.Object, "spec", field)
	values, _ := items.([]any)
	var out []map[string]any
	for _, v := range values {
		if m, ok := v.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func specNames(obj *unstructured.Unstructured, field string) []string {
	var names []string
	for _, c := range specContainers(obj, field) {
		names = append(names, str(c, "name"))
	}
	return names
}

// str is the string at path, or "" when it is missing or not a string.
func str(m map[string]any, path ...string) string {
	v, _, _ := unstructured.NestedFieldNoCopy(m, path...)
	s, _ := v.(string)
	return s
}

// integer is the number at key, or 0 when it is missing or not a number.
func integer(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
