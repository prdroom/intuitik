package readonly

import "k8s.io/client-go/rest"

// Bypass fixture 3 (packet M1-T4 R4): rest.Interface.Post().
func postVia(c rest.Interface) *rest.Request {
	return c.Post()
}
