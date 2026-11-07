//go:build !linux && !windows && !darwin

package install

import "github.com/kardianos/service"

// controlService leaves every action to the service library.
func controlService(service.Service, string, string) (bool, error) { return false, nil }

// serviceStatus leaves the status to the service library.
func serviceStatus(string) (running, known bool) { return false, false }

// serviceExecutable returns "": the service managers here do not tell which
// executable a service runs.
func serviceExecutable(string) string { return "" }

// serviceOfProcess returns "": the service managers here do not tell which
// service runs a process.
func serviceOfProcess(int) string { return "" }

// ConfigureService sets the platform specific settings of c; there are none.
func ConfigureService(*service.Config) {}

// serviceFiles returns nothing: uninstall leaves no files behind here.
func serviceFiles(string) []string { return nil }

func serviceFilesRemoved() {}

func usesRcLinks() bool { return false }
