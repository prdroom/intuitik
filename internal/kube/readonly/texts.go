package readonly

import (
	"fmt"
	"strings"
)

// Texts of packet M1-T3a section 3.3. Tests compare them byte for byte. Each failure
// text is one line: it is printed once per attempt on stderr and returned unchanged as
// the message of the failure state (one constant, two places).
const (
	// K-OK
	textOK = "Connected to cluster %s (context %s)."
	// K-NK
	textNK = "No kubeconfig found. intuitik looked in: %s. Set KUBECONFIG to the path of your kubeconfig file, or run your cloud's login command to create one."
	// K-IV1
	textIV1 = "Cannot read the kubeconfig %s: %s. Fix the file, or set KUBECONFIG to a valid kubeconfig file."
	// K-IV2
	textIV2 = "The kubeconfig has no current context. Choose a context in the intuitik tab, or set one with: kubectl config use-context %s"
	// K-IV3
	textIV3 = "Context %s refers to a cluster or user that is not in the kubeconfig. Fix the kubeconfig, or choose another context."
	// K-IV4
	textIV4 = "The kubeconfig %s has no contexts. Run your cloud's login command to add one, or set KUBECONFIG to another kubeconfig file."
	// K-AU
	textAU = "The cluster %s rejected the credentials of context %s (HTTP 401). They may have expired. Log in again with your cloud's login command, then retry from the intuitik tab or restart intuitik."
	// K-NC
	textNC = "Context %s has no credentials for cluster %s. intuitik reads a cluster only with the credentials of your kubeconfig. Log in with your cloud's login command, then retry from the intuitik tab, or choose another context."
	// K-EX
	textEX = "Context %s signs in with the credential program %s, which this build of intuitik does not run. Choose another context."
	// K-AP
	textAP = `Context %s uses the auth provider "%s", which intuitik does not support. Update the kubeconfig with your cloud's current login command.`
	// K-TL
	textTL = "The API server %s of context %s presented a certificate that the kubeconfig does not trust: %s. Check that the kubeconfig belongs to this cluster."
	// K-UR
	textUR = "Cannot reach the API server %s of context %s: %s. Check your VPN or network connection, then retry from the intuitik tab or restart intuitik."
	// K-HS
	textHS = "The API server %s of context %s answered HTTP %d instead of the Kubernetes API. Check the server address in the kubeconfig, or retry from the intuitik tab if the server is busy."
	// K-UC
	textUC = "There is no context named %s in the kubeconfig."
)

// textTimeout is the <os error> of K-UR when W1 passes.
const textTimeout = "no answer within 10 s"

// oneLine keeps a run-time part on one line, so that every text stays one line.
func oneLine(s string) string {
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
}

// text formats a text with its run-time parts, each kept on one line.
func text(format string, parts ...any) string {
	for i, p := range parts {
		if s, ok := p.(string); ok {
			parts[i] = oneLine(s)
		}
	}
	return fmt.Sprintf(format, parts...)
}

// UnknownContext is the K-UC text of a refused switch.
func UnknownContext(name string) string {
	return text(textUC, name)
}
