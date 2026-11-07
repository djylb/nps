// Package runenv locates the run, install, log and config paths of the
// process, reports its uptime and serves pprof on request.
package runenv

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

func GetPath(filePath string) string {
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(GetRunPath(), filePath)
	}
	path, err := filepath.Abs(filePath)
	if err != nil {
		return filePath
	}
	return path
}

// FileExists reports whether the named file or directory exists.
func FileExists(name string) bool {
	if _, err := os.Stat(name); err != nil && os.IsNotExist(err) {
		return false
	}
	return true
}

var (
	// ConfPath overrides the install path when set.
	ConfPath string

	startTime = time.Now()
)

// GetRunPath Get the currently selected configuration file directory
// For non-Windows systems, select the /etc/nps as config directory if exist, or select ./
// windows system, select the C:\Program Files\nps as config directory if exist, or select ./
func GetRunPath() string {
	var path string
	if len(os.Args) == 1 {
		if !IsWindows() {
			dir, _ := filepath.Abs(filepath.Dir(os.Args[0])) //返回
			return dir + "/"
		}
		return "./"
	}
	if path = GetInstallPath(); !FileExists(path) {
		return GetAppPath()
	}
	return path
}

// GetInstallPath Different systems get different installation paths
func GetInstallPath() string {
	var path string

	if ConfPath != "" {
		return ConfPath
	}

	if IsWindows() {
		path = `C:\Program Files\nps`
	} else {
		path = "/etc/nps"
	}

	return path
}

// GetAppPath Get the absolute path to the running directory
func GetAppPath() string {
	if exePath, err := os.Executable(); err == nil {
		return filepath.Dir(exePath)
	}
	if path, err := filepath.Abs(filepath.Dir(os.Args[0])); err == nil {
		return path
	}
	return os.Args[0]
}

// IsWindows Determine whether the current system is a Windows system?
func IsWindows() bool {
	return runtime.GOOS == "windows"
}

// OnOpenWrt reports whether this is an OpenWrt system. Its devices often
// have little memory, /var is a RAM disk and /etc is flash, so nps and npc
// log nothing there unless told to, as the OpenWrt packages do.
var OnOpenWrt = sync.OnceValue(func() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	_, err := os.Stat("/etc/openwrt_release")
	return err == nil
})

// LogDefaults returns the default log mode, file size in MB and number of
// files: off, 1 and 1 on OpenWrt, else typ, size and files.
func LogDefaults(typ string, size, files int) (string, int, int) {
	if OnOpenWrt() {
		return "off", 1, 1
	}
	return typ, size, files
}

// GetLogPath interface log file path
func GetLogPath() string {
	var path string
	if IsWindows() {
		path = filepath.Join(GetAppPath(), "nps.log")
	} else {
		path = "/var/log/nps.log"
	}
	return path
}

// GetNpcLogPath interface npc log file path
func GetNpcLogPath() string {
	var path string
	if IsWindows() {
		path = filepath.Join(GetAppPath(), "npc.log")
	} else {
		path = "/var/log/npc.log"
	}
	return path
}

// GetConfigPath config file path
func GetConfigPath() string {
	var path string
	if IsWindows() {
		path = filepath.Join(GetAppPath(), "conf/npc.conf")
	} else {
		path = "conf/npc.conf"
	}
	return path
}

func ResolvePath(path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(GetRunPath(), path)
	}
	return path
}

func GetRunTime() string {
	totalSecs := int64(time.Since(startTime).Seconds())
	days := totalSecs / 86400
	totalSecs %= 86400
	hours := totalSecs / 3600
	totalSecs %= 3600
	mins := totalSecs / 60
	secs := totalSecs % 60
	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if mins > 0 {
		parts = append(parts, fmt.Sprintf("%dm", mins))
	}
	if secs > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", secs))
	}
	return strings.Join(parts, " ")
}

func GetRunSecs() int64 {
	return int64(time.Since(startTime).Seconds())
}

func GetStartTime() int64 {
	return startTime.Unix()
}

// GetEnvMap get env
func GetEnvMap() map[string]string {
	m := make(map[string]string)
	environ := os.Environ()
	for i := range environ {
		tmp := strings.Split(environ[i], "=")
		if len(tmp) == 2 {
			m[tmp[0]] = tmp[1]
		}
	}
	return m
}
