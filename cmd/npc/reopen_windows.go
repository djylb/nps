//go:build windows && !sdk

package main

// watchReopen does nothing: Windows has no SIGHUP, and its files are
// rotated by the logger itself.
func watchReopen() {}
