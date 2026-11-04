package server

import "strings"

// Terminal texts of packet M1-T2 section 3.2. Tests compare them byte for byte.
const (
	textListen = "listening on http://127.0.0.1:%d" // S-L, the T1 listen-address line

	TextTerminal    = "stub S-E1" // S-E1
	TextNotTerminal = "stub S-E2" // S-E2

	TextOpenFailed = "stub S-O " // S-O, followed by the login address
	TextNewAddress = "stub S-R " // S-R, followed by the login address

	textNoLogin = `No browser has logged in within 60 seconds. If a tab opened, check that its address ended with "#t=" and the token: some programs that open links drop that part. A proxy for loopback addresses can also block the login. `
	// S-N1 (stdin is a terminal) and S-N2 (it is not).
	TextNoLoginTerminal    = "stub S-N1"
	TextNoLoginNotTerminal = "stub S-N2"

	TextNoFreePort = "stub S-P"            // S-P
	textBindFailed = "bind failed%.0s%.0s" // S-J1

	TextAlarm = "stub S-W" // S-W
)

// Browser text B-RF (section 3.3). The exchange-refusal 403 body is its three lines
// joined with "\n"; web/src/auth holds the same three lines for the screen.
var refusalLines = []string{"stub B-RF"}

// RefusalBody is the body of the exchange-refusal 403 (S17 (b)).
var RefusalBody = strings.Join(refusalLines, "\n")
