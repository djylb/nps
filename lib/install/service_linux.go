//go:build linux

package install

import (
	"os/exec"
	"strconv"

	"github.com/kardianos/service"
)

// controlService drives SysV init systems through the init script directly,
// so BusyBox based systems such as OpenWrt work as well; it leaves the other
// init systems to the service library.
func controlService(_ service.Service, name, action string) (bool, error) {
	switch p := host.platform(); {
	case p == "unix-systemv", p == "linux-procd" && action == "restart":
		// procd services restart through their init script, whoever
		// wrote it.
		return true, exec.Command("/etc/init.d/"+name, action).Run()
	}
	return false, nil
}

// serviceStatus asks the SysV init script whether the service runs; known is
// false on the other init systems.
func serviceStatus(name string) (running, known bool) {
	if host.platform() != "unix-systemv" {
		return false, false
	}
	return exec.Command("/etc/init.d/"+name, "status").Run() == nil, true
}

// serviceExecutable returns the executable the installed service runs, or ""
// when the init system does not tell.
func serviceExecutable(name string) string {
	switch host.platform() {
	case "linux-systemd":
		out, err := host.output("systemctl", "show", "-p", "ExecStart", "--value", name+".service")
		if err != nil {
			return ""
		}
		return parseSystemdExecStart(string(out))
	case "linux-procd":
		// procd knows the command of a running instance, also for init
		// scripts not written by install, such as those of OpenWrt packages.
		if p := procdExecutable(procdServices(), name); p != "" {
			return p
		}
		b, err := host.readFile("/etc/init.d/" + name)
		if err != nil {
			return ""
		}
		return parseSysvCmd(string(b))
	case "unix-systemv":
		b, err := host.readFile("/etc/init.d/" + name)
		if err != nil {
			return ""
		}
		return parseSysvCmd(string(b))
	}
	return ""
}

// serviceOfProcess returns the service that runs the process pid, or "" when
// it runs outside the service manager or the manager does not tell: systemd
// places every service in a cgroup named after it, the System V and procd
// scripts write the PID file of the service.
func serviceOfProcess(pid int) string {
	switch host.platform() {
	case "linux-systemd":
		b, err := host.readFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
		if err != nil {
			return ""
		}
		return parseCgroupUnit(string(b))
	case "linux-procd":
		return procdServiceOf(procdServices(), pid)
	case "unix-systemv":
		return pidFileService(pid)
	}
	return ""
}

// ConfigureService sets the platform specific settings of c.
func ConfigureService(c *service.Config) {
	if host.platform() != "linux-systemd" {
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
// system. The start and stop links are removeRcLinks' part.
func serviceFiles(name string) []string {
	switch host.platform() {
	case "linux-systemd":
		return []string{"/etc/systemd/system/" + name + ".service"}
	case "linux-procd":
		return []string{"/etc/init.d/" + name}
	}
	return nil
}

// serviceFilesRemoved tells the init system that UninstallService deleted the
// files of a service.
func serviceFilesRemoved() {
	if host.platform() == "linux-systemd" {
		_ = exec.Command("systemctl", "daemon-reload").Run()
	}
}

// usesRcLinks reports whether the init system starts services through
// start and stop links to their init scripts.
func usesRcLinks() bool {
	p := host.platform()
	return p == "unix-systemv" || p == "linux-procd"
}
