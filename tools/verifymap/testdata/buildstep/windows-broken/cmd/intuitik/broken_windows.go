//go:build windows

// Fixture for the build step test: the windows targets fail to compile.
package main

var _ = undefinedOnWindows
