package readonly

import (
	"net/url"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// Bypass fixture 5 (packet M1-T4 R4): an executor whose method is a string argument.
func execVia(cfg *rest.Config, u *url.URL) (remotecommand.Executor, error) {
	return remotecommand.NewSPDYExecutor(cfg, "POST", u)
}
