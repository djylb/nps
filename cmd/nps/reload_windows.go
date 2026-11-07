package main

// watchReload does nothing: Windows has no reload signal.
func watchReload(func()) (stop func()) { return func() {} }
