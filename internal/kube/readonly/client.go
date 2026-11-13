package readonly

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// Client is the read-only client of the active context. It has no package-level
// instance, getter or other constructor: internal/server creates it only in its
// cluster gate and hands it to the handler of a cluster route (section 6.2). Every
// request it sends passes the request guard, and every Secret it returns has its
// values removed (packet M1-T4 R5 and R6).
type Client struct {
	http      *http.Client
	host      string
	dynamic   dynamic.Interface
	discovery discovery.CachedDiscoveryInterface
}

// Resource names one resource type: its API group ("" for the core group), version
// and plural resource name.
type Resource struct {
	Group    string
	Version  string
	Resource string
}

// NewClient is the client constructor. It needs a connected Outcome.
func NewClient(o Outcome) (*Client, error) {
	if o.State != StateConnected || o.config == nil {
		return nil, errors.New("readonly: the active context is not connected")
	}
	return newClient(o.config)
}

// newClient builds the client of a guarded configuration.
func newClient(cfg *rest.Config) (*Client, error) {
	hc, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{
		http:      hc,
		host:      strings.TrimRight(cfg.Host, "/"),
		dynamic:   dyn,
		discovery: memory.NewMemCacheClient(dc), // discovery is cached in memory only (R9)
	}, nil
}

// Get sends one GET for an API path of the active context.
func (c *Client) Get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.host+path, nil)
	if err != nil {
		return nil, err
	}
	return c.http.Do(req)
}

func (c *Client) resource(r Resource, namespace string) dynamic.ResourceInterface {
	ri := c.dynamic.Resource(schema.GroupVersionResource{Group: r.Group, Version: r.Version, Resource: r.Resource})
	if namespace != "" {
		return ri.Namespace(namespace)
	}
	return ri
}

// Object reads one object; namespace is "" for a cluster-scoped resource.
func (c *Client) Object(ctx context.Context, r Resource, namespace, name string) (*unstructured.Unstructured, error) {
	return c.resource(r, namespace).Get(ctx, name, metav1.GetOptions{})
}

// List lists the objects of a resource; namespace "" lists every namespace.
func (c *Client) List(ctx context.Context, r Resource, namespace string) (*unstructured.UnstructuredList, error) {
	return c.resource(r, namespace).List(ctx, metav1.ListOptions{})
}

// Watch watches the objects of a resource; namespace "" watches every namespace.
func (c *Client) Watch(ctx context.Context, r Resource, namespace string) (watch.Interface, error) {
	return c.resource(r, namespace).Watch(ctx, metav1.ListOptions{})
}

// PodLog reads the log of one container of a Pod.
func (c *Client) PodLog(ctx context.Context, namespace, pod, container string) (io.ReadCloser, error) {
	path := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods/" + url.PathEscape(pod) + "/log"
	if container != "" {
		path += "?container=" + url.QueryEscape(container)
	}
	resp, err := c.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, errors.New("readonly: pod log: " + resp.Status)
	}
	return resp.Body, nil
}

// Resources reads the discovery documents: the resource lists of every group version.
func (c *Client) Resources() ([]*metav1.APIResourceList, error) {
	_, lists, err := c.discovery.ServerGroupsAndResources()
	return lists, err
}
