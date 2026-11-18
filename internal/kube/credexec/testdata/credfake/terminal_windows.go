//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// readTerminal opens the console input and waits for a key. Without a console the open
// fails and the program exits 3 at once.
func readTerminal() {
	f, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		os.Exit(3)
	}
	b := make([]byte, 1)
	f.Read(b)
}

var procQueryInformationJobObject = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryInformationJobObject")

// jobHandles counts the handle values of this process that name a job object: a handle
// that QueryInformationJobObject accepts for JobObjectBasicAccountingInformation. The
// job the process runs in gives it no handle; a count above 0 means a job handle was
// inherited.
func jobHandles() int {
	var info [64]byte
	n := 0
	for h := uintptr(4); h < 1<<16; h += 4 {
		r, _, _ := procQueryInformationJobObject.Call(h, 1, uintptr(unsafe.Pointer(&info[0])), uintptr(len(info)), 0)
		if r != 0 {
			n++
		}
	}
	return n
}
