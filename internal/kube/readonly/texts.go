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
	// K-EP (packet M1-T3b section 3)
	textEP = "The credential program %s of context %s failed (%s). Run it in a terminal to see why, or log in again with your cloud's login command."
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

// textTimeout is the <os error> of K-UR when W1 passes after the kubeconfig was read, and
// the K-EP reason of a run that W1 or its own 10 s ended.
const textTimeout = "no answer within 10 s"

// textKubeconfigTimeout is the <os error> of K-UR when W1 passes before the kubeconfig
// was read (UR-K, packet M1-T3b section 3).
const textKubeconfigTimeout = "the kubeconfig was not read within 10 s"

// textUnknown fills a server, cluster or context slot that has no value (UV).
const textUnknown = "(unknown)"

// slot is the value of a server, cluster or context slot: "(unknown)" for an empty one.
func slot(s string) string {
	if s == "" {
		return textUnknown
	}
	return s
}

// The texts with a server, cluster or context slot, formatted with the UV rule. Every
// other run-time part is given as it is.

func kOK(cluster, context string) string { return text(textOK, slot(cluster), slot(context)) }
func kIV3(context string) string         { return text(textIV3, slot(context)) }
func kAU(cluster, context string) string { return text(textAU, slot(cluster), slot(context)) }
func kNC(context, cluster string) string { return text(textNC, slot(context), slot(cluster)) }
func kAP(context, provider string) string {
	return text(textAP, slot(context), provider)
}
func kTL(server, context, msg string) string {
	return text(textTL, slot(server), slot(context), msg)
}
func kUR(server, context, osErr string) string {
	return text(textUR, slot(server), slot(context), osErr)
}
func kHS(server, context string, status int) string {
	return text(textHS, slot(server), slot(context), status)
}
func kEP(command, context, reason string) string {
	return text(textEP, command, slot(context), reason)
}

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
	return text(textUC, slot(name))
}
