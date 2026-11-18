package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

// cluster is the connection state of the active context (packet M1-T3a sections 3.2
// and 6.1). Every field is guarded by mu.
type cluster struct {
	mu         sync.Mutex
	outcome    readonly.Outcome // the last attempt that finished and was not canceled
	connecting bool
	target     string // the context of the running attempt; "" follows the kubeconfig
	override   string // the context chosen by a switch; "" follows the kubeconfig
	gen        int
	cancel     context.CancelFunc
	generation *readonly.Generation // the current generation (packet M1-T3b E5)
	print      func(o readonly.Outcome)
	// attempt runs one connection attempt; nil is the attempt of the new generation.
	// started, when set, runs after start has launched an attempt and before it
	// returns. Tests set both to put the attempt's publication before the handler's
	// answer.
	attempt func(ctx context.Context, name string) readonly.Outcome
	started func()
	// run runs credential programs; nil is runCredentialProgram.
	run readonly.Runner
}

// contextObject is one entry of the contexts list of the cluster object.
type contextObject struct {
	Name    string `json:"name"`
	Cluster string `json:"cluster"`
	Server  string `json:"server"`
	Current bool   `json:"current"`
	Active  bool   `json:"active"`
}

// clusterObject is the API answer of section 6.1; the U3 screen reads these names.
type clusterObject struct {
	State           string          `json:"state"`
	TextID          *string         `json:"text_id"`
	Message         *string         `json:"message"`
	Context         *string         `json:"context"`
	Cluster         *string         `json:"cluster"`
	Server          *string         `json:"server"`
	KubeconfigPaths []string        `json:"kubeconfig_paths"`
	Contexts        []contextObject `json:"contexts"`
}

func orNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// start begins a new attempt for name ("" follows the kubeconfig) in a new generation.
// The previous generation is closed under the cluster lock (E5): it is marked closed by
// the new generation number, its credential source is closed so that no program starts
// any more, and its context is canceled, which ends its running programs. A canceled
// attempt is never shown, and a state change of a closed generation is dropped.
// It returns the cluster object in state connecting, taken under the same lock that
// marks the attempt running, so the 202 answer of section 6.1 never shows a result
// that the attempt published before the handler wrote its answer.
func (c *cluster) start(name string) clusterObject {
	c.mu.Lock()
	c.gen++
	gen := c.gen
	if c.generation != nil {
		c.generation.Close()
	}
	if c.cancel != nil {
		c.cancel()
	}
	// ctx is the context of an attempt hook; the generation has its own.
	ctx, cancel := context.WithCancel(context.Background())
	run := c.run
	if run == nil {
		run = runCredentialProgram
	}
	g := readonly.NewGeneration(context.Background(), run, func(o readonly.Outcome) { c.changed(gen, o) })
	c.generation = g
	c.cancel, c.connecting, c.target = cancel, true, name
	attempt := c.attempt
	if attempt == nil {
		attempt = func(_ context.Context, name string) readonly.Outcome { return g.Attempt(name) }
	}
	_, obj := c.snapshotLocked()
	c.mu.Unlock()
	go func() {
		o := attempt(ctx, name)
		// The line is printed under the same lock that checked the generation, so a
		// newer start cannot come between the check and the line.
		c.mu.Lock()
		defer c.mu.Unlock()
		if o.Canceled || gen != c.gen {
			return
		}
		c.outcome, c.connecting = o, false
		c.print(o)
	}()
	if c.started != nil {
		c.started()
	}
	return obj
}

// stop closes the current generation when intuitik stops, so that its running programs
// are ended and none starts (E5).
func (c *cluster) stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	if c.generation != nil {
		c.generation.Close()
	}
	if c.cancel != nil {
		c.cancel()
	}
}

// changed applies a state change after connected that generation gen reported: a
// refresh that failed (K-EP) or credentials rejected again (K-AU). It is dropped unless
// gen is still the current generation and its attempt connected; the line is printed
// once (EP-1), under the same lock that checked the generation, so a switch or retry
// cannot come between the check and the line.
func (c *cluster) changed(gen int, o readonly.Outcome) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen || c.connecting || c.outcome.State != readonly.StateConnected {
		return
	}
	c.outcome = o
	c.print(o)
}

// snapshot returns the last outcome and the cluster object of the current state.
func (c *cluster) snapshot() (readonly.Outcome, clusterObject) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

// admit is snapshot plus the generation number of that state, read under one lock: the
// generation that a graph call is bound to (packet FA2-T5, CLIENT-PER-REQ-T5 point 1).
func (c *cluster) admit() (readonly.Outcome, clusterObject, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	o, obj := c.snapshotLocked()
	return o, obj, c.gen
}

// current reports that generation gen is still the cluster's and that its state is still
// connected (CLIENT-PER-REQ-T5 point 3). The lock is released before it returns, so no
// caller holds it while it makes a client (point 4).
func (c *cluster) current(gen int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return gen == c.gen && !c.connecting && c.outcome.State == readonly.StateConnected
}

// snapshotLocked is snapshot for a caller that holds mu.
func (c *cluster) snapshotLocked() (readonly.Outcome, clusterObject) {
	o := c.outcome
	obj := clusterObject{
		State:           string(o.State),
		KubeconfigPaths: append([]string{}, o.Paths...),
		Contexts:        []contextObject{},
	}
	active := o.Context
	if c.connecting {
		active = c.target
		if active == "" {
			active = o.Current
		}
		obj.State = string(readonly.StateConnecting)
		obj.Context = orNull(active)
		for _, e := range o.Contexts {
			if e.Name == active {
				obj.Cluster, obj.Server = orNull(e.Cluster), orNull(e.Server)
			}
		}
		o.State = readonly.StateConnecting
	} else {
		obj.TextID, obj.Message = orNull(o.TextID), orNull(o.Message)
		obj.Context, obj.Cluster, obj.Server = orNull(o.Context), orNull(o.Cluster), orNull(o.Server)
	}
	for _, e := range o.Contexts {
		obj.Contexts = append(obj.Contexts, contextObject{
			Name: e.Name, Cluster: e.Cluster, Server: e.Server,
			Current: e.Name == o.Current, Active: e.Name == active,
		})
	}
	return o, obj
}

// listed reports whether name is a context of the last finished attempt.
func (c *cluster) listed(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.outcome.Contexts {
		if e.Name == name {
			return true
		}
	}
	return false
}

// handleCluster is GET /api/cluster: the cluster object in every state.
func (s *server) handleCluster(w http.ResponseWriter, r *http.Request) {
	_, obj := s.cluster.snapshot()
	writeJSON(w, obj)
}

// handleClusterContext is POST /api/cluster/context with {"name": "<context>"}. The
// kubeconfig's current context is never changed (W4).
func (s *server) handleClusterContext(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err == nil {
		json.Unmarshal(raw, &req)
	}
	if !s.cluster.listed(req.Name) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"text_id": "K-UC", "message": readonly.UnknownContext(req.Name)})
		return
	}
	s.cluster.mu.Lock()
	s.cluster.override = req.Name
	s.cluster.mu.Unlock()
	writeJSONStatus(w, http.StatusAccepted, s.cluster.start(req.Name))
}

// handleClusterRetry is POST /api/cluster/retry: a new attempt for the active context.
func (s *server) handleClusterRetry(w http.ResponseWriter, r *http.Request) {
	s.cluster.mu.Lock()
	name := s.cluster.override
	s.cluster.mu.Unlock()
	writeJSONStatus(w, http.StatusAccepted, s.cluster.start(name))
}

// printOutcome prints the text of a finished attempt once: K-OK on stdout, every
// failure text on stderr.
func (s *server) printOutcome(o readonly.Outcome) {
	if o.Stdout() {
		s.println(s.o.Stdout, o.Message)
		return
	}
	s.println(s.o.Stderr, o.Message)
}
