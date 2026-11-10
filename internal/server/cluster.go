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
	print      func(o readonly.Outcome)
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

// start begins a new attempt for name ("" follows the kubeconfig). A running attempt
// is canceled: the most recent request wins and a canceled attempt is never shown.
func (c *cluster) start(name string) {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.gen++
	gen := c.gen
	c.cancel, c.connecting, c.target = cancel, true, name
	c.mu.Unlock()
	go func() {
		defer cancel()
		o := readonly.Attempt(ctx, name)
		c.mu.Lock()
		if o.Canceled || gen != c.gen {
			c.mu.Unlock()
			return
		}
		c.outcome, c.connecting = o, false
		c.mu.Unlock()
		c.print(o)
	}()
}

// snapshot returns the last outcome and the cluster object of the current state.
func (c *cluster) snapshot() (readonly.Outcome, clusterObject) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	s.cluster.start(req.Name)
	_, obj := s.cluster.snapshot()
	writeJSONStatus(w, http.StatusAccepted, obj)
}

// handleClusterRetry is POST /api/cluster/retry: a new attempt for the active context.
func (s *server) handleClusterRetry(w http.ResponseWriter, r *http.Request) {
	s.cluster.mu.Lock()
	name := s.cluster.override
	s.cluster.mu.Unlock()
	s.cluster.start(name)
	_, obj := s.cluster.snapshot()
	writeJSONStatus(w, http.StatusAccepted, obj)
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
