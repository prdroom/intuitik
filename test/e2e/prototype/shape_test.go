// Package prototype checks the fixed data of the relation map prototype (packet FA2-T8 6.2):
// every graph answer under web/prototype/data and every member of the T12 L graph file
// decodes strictly into the server's graph types, with every required field, bridges that
// leave a node of the same answer and states the graph package defines; the scenario list
// names what the answers hold; and the canary table matches the Secret object files while no
// canary is in any graph answer or in the prototype's string dictionary.
package prototype

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/prdroom/intuitik/internal/graph"
)

const (
	dataDir   = "../../../web/prototype/data"
	stringsJS = "../../../web/prototype/strings.js"
	lFile     = "testdata/l-graph.tar.gz"
	lSHA256   = "6a1bd4382805a3a66e78ac8d3a101bbfdf4943eca386b47a1cd84db07d793634"
	lMembers  = 57
)

// strict decodes data into v with unknown fields refused and no trailing token.
func strict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing data after the answer")
	}
	return nil
}

// required lists, per object of an answer, the keys the server always writes.
var (
	answerKeys  = []string{"namespace", "namespaceState", "nodes", "refused", "bridges", "unsupported"}
	stateKeys   = []string{"state", "existenceChecked"}
	nodeKeys    = []string{"uid", "apiVersion", "kind", "name", "parent", "owner"}
	bridgeKeys  = []string{"kind", "from", "to", "state", "settings"}
	targetKeys  = []string{"group", "namespace", "kind", "name", "uid"}
	clusterKeys = []string{"namespaces", "band", "refused"}
	listKeys    = []string{"listed", "items", "from"}
)

func missing(obj map[string]any, keys []string) []string {
	var out []string
	for _, k := range keys {
		if _, ok := obj[k]; !ok {
			out = append(out, k)
		}
	}
	return out
}

func requireKeys(data []byte, cluster bool) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if cluster {
		if m := missing(raw, clusterKeys); len(m) > 0 {
			return fmt.Errorf("cluster answer misses %v", m)
		}
		if m := missing(raw["namespaces"].(map[string]any), listKeys); len(m) > 0 {
			return fmt.Errorf("namespaces misses %v", m)
		}
		return nil
	}
	if m := missing(raw, answerKeys); len(m) > 0 {
		return fmt.Errorf("answer misses %v", m)
	}
	if m := missing(raw["namespaceState"].(map[string]any), stateKeys); len(m) > 0 {
		return fmt.Errorf("namespaceState misses %v", m)
	}
	for _, n := range raw["nodes"].([]any) {
		node := n.(map[string]any)
		if m := missing(node, nodeKeys); len(m) > 0 {
			return fmt.Errorf("node %v misses %v", node["uid"], m)
		}
		_, has := node["nodeState"]
		if isPod := node["kind"] == "Pod" && node["apiVersion"] == "v1"; isPod != has {
			return fmt.Errorf("node %v: nodeState present=%v for kind %v", node["uid"], has, node["kind"])
		}
	}
	for _, b := range raw["bridges"].([]any) {
		bridge := b.(map[string]any)
		if m := missing(bridge, bridgeKeys); len(m) > 0 {
			return fmt.Errorf("bridge misses %v", m)
		}
		if m := missing(bridge["to"].(map[string]any), targetKeys); len(m) > 0 {
			return fmt.Errorf("bridge target misses %v", m)
		}
	}
	return nil
}

var (
	bridgeStates = set(graph.BridgeBroken, graph.BridgeWarning, graph.BridgeUnknown, graph.BridgeWaiting, graph.BridgeOptional, graph.BridgeConnected)
	bridgeKinds  = set(graph.BridgeIngressService, graph.BridgeServicePod, graph.BridgePodPVC, graph.BridgePVCPV, graph.BridgePodConfigMap,
		graph.BridgePodSecret, graph.BridgeHPATarget, graph.BridgeHTTPRouteService, graph.BridgePVCStorageClass, graph.BridgePVStorageClass)
	nodeStates      = set(graph.NodeHealthy, graph.NodeProgressing, graph.NodeFailing, graph.NodeUnknown)
	namespaceStates = set(graph.NamespaceOK, graph.NamespaceEmpty, graph.NamespaceNotFound, graph.NamespaceNoPermission)
	ownerStates     = set(string(graph.StateNone), string(graph.StateContained), string(graph.StateMissing), string(graph.StateForbidden),
		string(graph.StateClusterScoped), string(graph.StateLoop))
)

func set(values ...string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}

// checkAnswer decodes one namespace answer and checks its references and states.
func checkAnswer(name string, data []byte) (graph.Answer, error) {
	var a graph.Answer
	if err := strict(data, &a); err != nil {
		return a, fmt.Errorf("%s: %w", name, err)
	}
	if err := requireKeys(data, false); err != nil {
		return a, fmt.Errorf("%s: %w", name, err)
	}
	if !namespaceStates[a.NamespaceState.State] {
		return a, fmt.Errorf("%s: namespace state %q", name, a.NamespaceState.State)
	}
	uids := map[string]bool{}
	for _, n := range a.Nodes {
		uids[n.UID] = true
		if !ownerStates[string(n.Owner.State)] {
			return a, fmt.Errorf("%s: node %s owner state %q", name, n.UID, n.Owner.State)
		}
		if n.NodeState != nil && !nodeStates[n.NodeState.State] {
			return a, fmt.Errorf("%s: node %s state %q", name, n.UID, n.NodeState.State)
		}
	}
	for i, b := range a.Bridges {
		if !uids[b.From.UID] {
			return a, fmt.Errorf("%s: bridge %d from.uid %s is not a node of the answer", name, i, b.From.UID)
		}
		if !bridgeStates[b.State] || !bridgeKinds[b.Kind] {
			return a, fmt.Errorf("%s: bridge %d kind %q state %q", name, i, b.Kind, b.State)
		}
	}
	return a, nil
}

func checkCluster(name string, data []byte) error {
	var c graph.ClusterAnswer
	if err := strict(data, &c); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return requireKeys(data, true)
}

func readData(t *testing.T) (map[string][]byte, map[string]graph.Answer) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dataDir, "ns-*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no answers under %s: %v", dataDir, err)
	}
	raw := map[string][]byte{}
	answers := map[string]graph.Answer{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		a, err := checkAnswer(filepath.Base(f), data)
		if err != nil {
			t.Fatal(err)
		}
		raw[filepath.Base(f)] = data
		answers[a.Namespace] = a
	}
	return raw, answers
}

func TestPrototypeData_Answers(t *testing.T) {
	_, answers := readData(t)
	data, err := os.ReadFile(filepath.Join(dataDir, "cluster.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkCluster("cluster.json", data); err != nil {
		t.Fatal(err)
	}
	var c graph.ClusterAnswer
	_ = json.Unmarshal(data, &c)
	var listed []string
	for ns := range answers {
		listed = append(listed, ns)
	}
	sort.Strings(listed)
	if strings.Join(listed, ",") != strings.Join(c.Namespaces.Items, ",") {
		t.Fatalf("cluster.json lists %v, the answers are %v", c.Namespaces.Items, listed)
	}
	t.Logf("prototype data: %d answers and cluster.json decode strictly", len(answers))
}

// scenario is one row of web/prototype/data/scenarios.json.
type scenario struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	Target    string `json:"target"`
	Kind      string `json:"kind"`
	From      string `json:"from"`
	Name      string `json:"name"`
	State     string `json:"state"`
	Text      string `json:"text"`
}

func TestPrototypeData_Scenarios(t *testing.T) {
	_, answers := readData(t)
	data, err := os.ReadFile(filepath.Join(dataDir, "scenarios.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Packet    string     `json:"packet"`
		Scenarios []scenario `json:"scenarios"`
	}
	if err := strict(data, &file); err != nil {
		t.Fatal(err)
	}
	want := []string{"A-0", "A-1", "A-2", "A-3", "A-4", "A-5", "A-6", "F-18", "owner-missing"}
	var got []string
	for _, s := range file.Scenarios {
		got = append(got, s.ID)
		if !found(answers[s.Namespace], s) {
			t.Errorf("scenario %s: %s %s %s in %s with state %q and text %q is not in the data", s.ID, s.Target, s.Kind, s.From+s.Name, s.Namespace, s.State, s.Text)
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("scenarios %v, want %v", got, want)
	}
}

func found(a graph.Answer, s scenario) bool {
	names := map[string]graph.Node{}
	for _, n := range a.Nodes {
		names[n.UID] = n
	}
	if s.Target == "node" {
		for _, n := range a.Nodes {
			if n.Kind != s.Kind || n.Name != s.Name {
				continue
			}
			if n.NodeState != nil && n.NodeState.State == s.State && n.NodeState.Text == s.Text {
				return true
			}
			if string(n.Owner.State) == s.State && n.Owner.Text == s.Text {
				return true
			}
		}
		return false
	}
	for _, b := range a.Bridges {
		if b.Kind == s.Kind && names[b.From.UID].Name == s.From && b.State == s.State && b.Text == s.Text {
			return true
		}
	}
	return false
}

// canary is one row of web/prototype/data/canaries.json.
type canary struct {
	Base64    string `json:"base64"`
	Canary    string `json:"canary"`
	Key       string `json:"key"`
	Namespace string `json:"namespace"`
	Readable  bool   `json:"readable"`
	Secret    string `json:"secret"`
	Use       string `json:"use"`
}

func TestPrototypeData_Canaries(t *testing.T) {
	raw, _ := readData(t)
	data, err := os.ReadFile(filepath.Join(dataDir, "canaries.json"))
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Packet string   `json:"packet"`
		Rows   []canary `json:"rows"`
	}
	if err := strict(data, &table); err != nil {
		t.Fatal(err)
	}
	rows := map[string]canary{}
	for _, r := range table.Rows {
		if base64.StdEncoding.EncodeToString([]byte(r.Canary)) != r.Base64 {
			t.Errorf("row %s/%s %s: base64 is not the canary's", r.Namespace, r.Secret, r.Key)
		}
		rows[r.Namespace+"/"+r.Secret+"/"+r.Key] = r
	}
	seen := map[string]bool{}
	secrets, _ := filepath.Glob(filepath.Join(dataDir, "objects", "*", "Secret", "*.json"))
	for _, f := range secrets {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var obj struct {
			Metadata   struct{ Name, Namespace string } `json:"metadata"`
			Data       map[string]string                 `json:"data"`
			StringData map[string]string                 `json:"stringData"`
		}
		if err := json.Unmarshal(body, &obj); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for key, v := range obj.Data {
			id := obj.Metadata.Namespace + "/" + obj.Metadata.Name + "/" + key
			if r, ok := rows[id]; !ok || r.Base64 != v {
				t.Errorf("%s: data %s is not the canary row's base64", f, key)
			}
			seen[id] = true
		}
		for key, v := range obj.StringData {
			id := obj.Metadata.Namespace + "/" + obj.Metadata.Name + "/" + key
			if r, ok := rows[id]; !ok || r.Canary != v {
				t.Errorf("%s: stringData %s is not the canary row's canary", f, key)
			}
		}
	}
	for id := range rows {
		if !seen[id] {
			t.Errorf("canary row %s is in no Secret object file", id)
		}
	}
	dictionary, err := os.ReadFile(stringsJS)
	if err != nil {
		t.Fatal(err)
	}
	cluster, err := os.ReadFile(filepath.Join(dataDir, "cluster.json"))
	if err != nil {
		t.Fatal(err)
	}
	places := map[string][]byte{"web/prototype/strings.js": dictionary, "web/prototype/data/cluster.json": cluster}
	for name, body := range raw {
		places["web/prototype/data/"+name] = body
	}
	for place, body := range places {
		for _, r := range table.Rows {
			if bytes.Contains(body, []byte(r.Canary)) || bytes.Contains(body, []byte(r.Base64)) {
				t.Errorf("%s holds the canary of %s/%s %s", place, r.Namespace, r.Secret, r.Key)
			}
		}
	}
	t.Logf("canaries: %d rows, %d Secret object files, none in %d answers or strings.js", len(rows), len(secrets), len(raw))
}

func TestPrototypeData_LGraph(t *testing.T) {
	gz, err := os.ReadFile(lFile)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(gz)
	if got := hex.EncodeToString(sum[:]); got != lSHA256 {
		t.Fatalf("%s sha256 %s, want %s", lFile, got, lSHA256)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	members := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimPrefix(h.Name, "./")
		members++
		if name == "cluster.json" {
			err = checkCluster(name, data)
		} else {
			_, err = checkAnswer(name, data)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if members != lMembers {
		t.Fatalf("%s has %d members, want %d", lFile, members, lMembers)
	}
	t.Logf("L graph: %d members decode strictly", members)
}
