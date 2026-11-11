package readonly

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"

	"k8s.io/client-go/rest"
)

// userAgentMarker is added to the User-Agent of every request that readonly sends
// (packet M1-T4 R5).
const userAgentMarker = "intuitik-readonly"

// errRefused is the error of every request that the guard refuses before sending.
var errRefused = errors.New("readonly: request refused")

// refusals counts the requests that the guard refused.
var refusals atomic.Int64

func refuse(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errRefused, fmt.Sprintf(format, args...))
}

// guarded returns a copy of cfg whose every request passes through the request guard
// and whose every response of a resource path passes through the Secret conversion
// (packet M1-T4 R5 and R6). Every rest.Config that readonly uses is made here.
func guarded(cfg *rest.Config) *rest.Config {
	c := rest.CopyConfig(cfg)
	c.ContentType = "application/json"
	c.AcceptContentTypes = "application/json"
	c.Wrap(func(rt http.RoundTripper) http.RoundTripper { return &guard{next: rt} })
	return c
}

// guard is the innermost wrapper of the client library's transport chain: the
// authentication, user agent and impersonation wrappers run before it, so it sees the
// request as it would be sent.
type guard struct {
	next http.RoundTripper
}

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	t, err := checkRequest(req)
	if err != nil {
		refusals.Add(1)
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, err
	}
	r := req.Clone(req.Context())
	// The transport below asks for and decompresses gzip itself, so the conversion
	// always reads the decoded body.
	r.Header.Del("Accept-Encoding")
	ua := strings.TrimSpace(r.Header.Get("User-Agent"))
	if ua == "" {
		ua = userAgentMarker
	} else {
		ua += " " + userAgentMarker
	}
	r.Header.Set("User-Agent", ua)
	resp, err := g.next.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	return convertResponse(resp, t, isWatch(r))
}

// checkRequest is the request guard: method GET, no upgrade, no impersonation and a
// path of the allowed grammar. It returns the interpreted path.
func checkRequest(req *http.Request) (target, error) {
	if req.Method != http.MethodGet {
		return target{}, refuse("method %s", req.Method)
	}
	if req.Header.Get("Upgrade") != "" || strings.Contains(strings.ToLower(req.Header.Get("Connection")), "upgrade") {
		return target{}, refuse("upgrade request")
	}
	for name := range req.Header {
		if strings.HasPrefix(strings.ToLower(name), "impersonate-") {
			return target{}, refuse("impersonation header %s", name)
		}
	}
	return classify(req.URL.EscapedPath())
}

func isWatch(req *http.Request) bool {
	w := req.URL.Query().Get("watch")
	return w == "true" || w == "1"
}

// Kinds of an allowed path.
const (
	kindDiscovery   = "discovery"
	kindNonResource = "non-resource"
	kindResource    = "resource"
	kindLog         = "log"
)

// target is a path interpreted by position (packet M1-T4 R5).
type target struct {
	kind      string
	group     string // "" for the core group
	version   string
	namespace string
	resource  string
	name      string
}

// secrets reports a path whose resource is the core secrets resource.
func (t target) secrets() bool {
	return t.kind == kindResource && t.group == "" && t.resource == "secrets"
}

var (
	versionSegment  = regexp.MustCompile(`^v[0-9]+((alpha|beta)[0-9]+)?$`)
	groupSegment    = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
	resourceSegment = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
	namespaceLabel  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

// classify interprets a raw (escaped) URL path. A non-canonical path is refused before
// interpretation: an empty, "." or ".." segment, a trailing "/" and any percent
// encoding. Upper case is refused in structural segments only (P-d, hoan 2026-11-10);
// a namespace follows the DNS-1123 label rule and a name the path-segment name rule.
func classify(raw string) (target, error) {
	if !strings.HasPrefix(raw, "/") || raw == "/" {
		return target{}, refuse("path %q", raw)
	}
	if strings.Contains(raw, "%") {
		return target{}, refuse("percent-encoded path %q", raw)
	}
	seg := strings.Split(raw[1:], "/")
	for _, s := range seg {
		if s == "" || s == "." || s == ".." {
			return target{}, refuse("non-canonical path %q", raw)
		}
	}
	switch seg[0] {
	case "version":
		if len(seg) == 1 {
			return target{kind: kindNonResource}, nil
		}
	case "openapi":
		if len(seg) == 2 && seg[1] == "v2" {
			return target{kind: kindNonResource}, nil
		}
		if len(seg) >= 2 && seg[1] == "v3" {
			return target{kind: kindNonResource}, nil
		}
	case "api":
		if len(seg) == 1 {
			return target{kind: kindDiscovery}, nil
		}
		if !versionSegment.MatchString(seg[1]) {
			return target{}, refuse("version segment in %q", raw)
		}
		if len(seg) == 2 {
			return target{kind: kindDiscovery, version: seg[1]}, nil
		}
		return classifyResource(raw, "", seg[1], seg[2:])
	case "apis":
		if len(seg) == 1 {
			return target{kind: kindDiscovery}, nil
		}
		if !groupSegment.MatchString(seg[1]) {
			return target{}, refuse("group segment in %q", raw)
		}
		if len(seg) == 2 {
			return target{kind: kindDiscovery, group: seg[1]}, nil
		}
		if !versionSegment.MatchString(seg[2]) {
			return target{}, refuse("version segment in %q", raw)
		}
		if len(seg) == 3 {
			return target{kind: kindDiscovery, group: seg[1], version: seg[2]}, nil
		}
		return classifyResource(raw, seg[1], seg[2], seg[3:])
	}
	return target{}, refuse("path %q is not in the allowed list", raw)
}

// classifyResource interprets the segments after the group version.
func classifyResource(raw, group, version string, rest []string) (target, error) {
	t := target{kind: kindResource, group: group, version: version}
	if rest[0] == "namespaces" && len(rest) >= 3 {
		if !namespaceLabel.MatchString(rest[1]) || len(rest[1]) > 63 {
			return target{}, refuse("namespace segment in %q", raw)
		}
		if rest[2] == "status" || rest[2] == "finalize" {
			return target{}, refuse("namespace subresource in %q", raw)
		}
		t.namespace = rest[1]
		rest = rest[2:]
	}
	if !resourceSegment.MatchString(rest[0]) || rest[0] == "watch" || rest[0] == "proxy" {
		return target{}, refuse("resource segment in %q", raw)
	}
	t.resource = rest[0]
	if len(rest) >= 2 {
		if t.resource == "namespaces" && t.namespace == "" && (!namespaceLabel.MatchString(rest[1]) || len(rest[1]) > 63) {
			return target{}, refuse("namespace segment in %q", raw)
		}
		t.name = rest[1]
	}
	switch {
	case len(rest) <= 2:
		return t, nil
	case len(rest) == 3 && t.namespace != "" && t.group == "" && t.resource == "pods" && rest[2] == "log":
		t.kind = kindLog
		return t, nil
	}
	return target{}, refuse("subresource in %q", raw)
}

// convertResponse removes Secret values from the response of a resource path. A
// resource response that is not JSON, or that arrives encoded, is refused.
func convertResponse(resp *http.Response, t target, watch bool) (*http.Response, error) {
	if t.kind != kindResource {
		return resp, nil
	}
	if enc := resp.Header.Get("Content-Encoding"); enc != "" && !strings.EqualFold(enc, "identity") {
		resp.Body.Close()
		return nil, refuse("encoded response %q", enc)
	}
	mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		resp.Body.Close()
		return nil, refuse("response media type %q", resp.Header.Get("Content-Type"))
	}
	if watch {
		resp.Body = newWatchConverter(resp.Body, t.secrets())
		resp.ContentLength = -1
		resp.Header.Del("Content-Length")
		return resp, nil
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	out, err := convertDocument(body, t.secrets())
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(strings.NewReader(string(out)))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", fmt.Sprint(len(out)))
	return resp, nil
}
