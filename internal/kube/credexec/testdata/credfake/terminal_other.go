//go:build !windows

package main

import "os"

// readTerminal opens the controlling terminal and waits for a line from it. Without a
// controlling terminal the open fails and the program exits 3 at once.
func readTerminal() {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		os.Exit(3)
	}
	b := make([]byte, 1)
	f.Read(b)
}

// jobHandles is Windows only.
func jobHandles() int { return -1 }
