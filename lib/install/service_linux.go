//go:build linux

package install

import (
	"os"
	"os/exec"

	"github.com/kardianos/service"
)

// controlService drives SysV init systems through the init script directly,
// so BusyBox based systems such as OpenWrt work as well; it leaves the other
// init systems to the service library.
func controlService(_ service.Service, name, action string) (bool, error) {
	if service.Platform() == "unix-systemv" {
		return true, exec.Command("/etc/init.d/"+name, action).Run()
	}
	return false, nil
}

// serviceStatus asks the SysV init script whether the service runs; known is
// false on the other init systems.
func serviceStatus(name string) (running, known bool) {
	if service.Platform() != "unix-systemv" {
		return false, false
	}
	return exec.Command("/etc/init.d/"+name, "status").Run() == nil, true
}

// serviceExecutable returns the executable the installed service runs, or ""
// when the init system does not tell.
func serviceExecutable(name string) string {
	switch service.Platform() {
	case "linux-systemd":
		out, err := exec.Command("systemctl", "show", "-p", "ExecStart", "--value", name+".service").Output()
		if err != nil {
			return ""
		}
		return parseSystemdExecStart(string(out))
	case "unix-systemv":
		b, err := os.ReadFile("/etc/init.d/" + name)
		if err != nil {
			return ""
		}
		return parseSysvCmd(string(b))
	}
	return ""
}

// ConfigureService sets the platform specific settings of c.
func ConfigureService(c *service.Config) {
	if service.Platform() != "linux-systemd" {
		return
	}
	c.Dependencies = []string{
		"Requires=network.target",
		"Wants=network-online.target",
		"After=network-online.target syslog.target"}
	c.Option["LimitNOFILE"] = 65536
}

// serviceFiles are the files the install of the init system creates, on the
// init systems whose uninstall deletes them only after running the init
// system.
func serviceFiles(name string) []string {
	switch service.Platform() {
	case "linux-systemd":
		return []string{"/etc/systemd/system/" + name + ".service"}
	case "linux-procd":
		return []string{"/etc/init.d/" + name, "/etc/rc.d/S50" + name, "/etc/rc.d/K02" + name}
	}
	return nil
}

// serviceFilesRemoved tells the init system that UninstallService deleted the
// files of a service.
func serviceFilesRemoved() {
	if service.Platform() == "linux-systemd" {
		_ = exec.Command("systemctl", "daemon-reload").Run()
	}
}
