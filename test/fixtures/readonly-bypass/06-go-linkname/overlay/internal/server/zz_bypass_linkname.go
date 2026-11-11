package server

import _ "unsafe"

// Bypass fixture 6 (packet M1-T4 R4): go:linkname in a product package.
//
//go:linkname runtimeNano runtime.nanotime
func runtimeNano() int64
