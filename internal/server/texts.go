package server

import "strings"

// Terminal texts of packet M1-T2 section 3.2. Tests compare them byte for byte.
const (
	textListen = "listening on http://127.0.0.1:%d" // S-L, the T1 listen-address line

	TextTerminal    = "Press Enter to open a new login address in your browser."                                                                // S-E1
	TextNotTerminal = "Standard input is not a terminal, so pressing Enter cannot open a new login address. To log in again, restart intuitik." // S-E2

	TextOpenFailed = "Could not open a browser. Open this address within 60 seconds: " // S-O, followed by the login address
	TextNewAddress = "New login address (valid once, for 60 seconds): "                // S-R, followed by the login address

	textNoLogin = `No browser has logged in within 60 seconds. If a tab opened, check that its address ended with "#t=" and the token: some programs that open links drop that part. A proxy for loopback addresses can also block the login. `
	// S-N1 (stdin is a terminal) and S-N2 (it is not).
	TextNoLoginTerminal    = textNoLogin + "Press Enter for a new login address."
	TextNoLoginNotTerminal = textNoLogin + "Restart intuitik for a new login address."

	TextNoFreePort = "Could not find a free port on 127.0.0.1 after 20 tries (range 49152-65535). Close programs that listen on many local ports, then run intuitik again." // S-P
	textBindFailed = "Cannot listen on 127.0.0.1: %s. Check the loopback interface with: %s"                                                                                // S-J1

	TextAlarm = "WARNING: someone else may have connected first. Restart intuitik; pressing Enter does not remove their session." // S-W
)

// Browser text B-RF (section 3.3). The exchange-refusal 403 body is its three lines
// joined with "\n"; web/src/auth holds the same three lines for the screen.
var refusalLines = []string{
	"This login address is no longer valid. It was already used, or it expired after 60 seconds.",
	"Press Enter in the terminal where intuitik is running to open a new login address, or restart intuitik.",
	`If your terminal shows "someone else may have connected first", restart intuitik instead of pressing Enter.`,
}

// RefusalBody is the body of the exchange-refusal 403 (S17 (b)).
var RefusalBody = strings.Join(refusalLines, "\n")
