package readonly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	if o.gen != nil {
		return o.gen.client(o.config)
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

// metadataAccept asks the API server for a list in the metadata-only form (packet FA2-T7a
// 6.2): a PartialObjectMetadataList, whose items carry no data.
const metadataAccept = "application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1"

// metadataLimit bounds the bytes read of one metadata-only list.
const metadataLimit = 64 << 20

// ObjectMeta is the identity of one listed object. Labels, annotations and every other
// field stay inside this package: the last-applied annotation of an object can hold its
// data (packet FA2-T7a 6.2).
type ObjectMeta struct {
	Name      string
	Namespace string
	UID       string
}

// ListMetadata lists the objects of one namespaced resource as metadata only, through the
// guarded client (packet FA2-T7a 6.2). A 200 answer whose media type is not
// application/json or whose body is not a meta.k8s.io/v1 PartialObjectMetadataList is an
// error, never read as a plain list. Any other HTTP status gives the API status error
// that List gives, with that status code.
func (c *Client) ListMetadata(ctx context.Context, r Resource, namespace string) ([]ObjectMeta, error) {
	path := "/api/" + url.PathEscape(r.Version)
	if r.Group != "" {
		path = "/apis/" + url.PathEscape(r.Group) + "/" + url.PathEscape(r.Version)
	}
	path += "/namespaces/" + url.PathEscape(namespace) + "/" + url.PathEscape(r.Resource)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.host+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", metadataAccept)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, metadataLimit))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, statusError(resp.StatusCode, body)
	}
	if media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || media != "application/json" {
		return nil, fmt.Errorf("readonly: metadata list of %s: media type %q is not application/json", r.Resource, resp.Header.Get("Content-Type"))
	}
	var list struct {
		Kind       string `json:"kind"`
		APIVersion string `json:"apiVersion"`
		Items      []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
				UID       string `json:"uid"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("readonly: metadata list of %s: %v", r.Resource, err)
	}
	if list.Kind != "PartialObjectMetadataList" || list.APIVersion != "meta.k8s.io/v1" {
		return nil, fmt.Errorf("readonly: metadata list of %s: answer is %s %s, not meta.k8s.io/v1 PartialObjectMetadataList", r.Resource, list.APIVersion, list.Kind)
	}
	out := make([]ObjectMeta, 0, len(list.Items))
	for _, it := range list.Items {
		out = append(out, ObjectMeta{Name: it.Metadata.Name, Namespace: it.Metadata.Namespace, UID: it.Metadata.UID})
	}
	return out, nil
}

// statusError is the API status error of a non-2xx answer, as the client library gives
// it: the Status body when there is one, always with the HTTP status code received.
func statusError(code int, body []byte) error {
	var st metav1.Status
	if json.Unmarshal(body, &st) != nil || st.Kind != "Status" {
		st = metav1.Status{Status: metav1.StatusFailure, Message: http.StatusText(code)}
	}
	st.Code = int32(code)
	return &apierrors.StatusError{ErrStatus: st}
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
