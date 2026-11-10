package readonly

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"k8s.io/client-go/rest"
)

// Client is the read-only client of the active context. It has no package-level
// instance, getter or other constructor: internal/server creates it only in its
// cluster gate and hands it to the handler of a cluster route (section 6.2).
type Client struct {
	http *http.Client
	host string
}

// NewClient is the client constructor. It needs a connected Outcome.
func NewClient(o Outcome) (*Client, error) {
	if o.State != StateConnected || o.config == nil {
		return nil, errors.New("readonly: the active context is not connected")
	}
	hc, err := rest.HTTPClientFor(o.config)
	if err != nil {
		return nil, err
	}
	return &Client{http: hc, host: strings.TrimRight(o.config.Host, "/")}, nil
}

// Get sends one GET for an API path of the active context.
func (c *Client) Get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.host+path, nil)
	if err != nil {
		return nil, err
	}
	return c.http.Do(req)
}
