package install

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/c4milo/unpackit"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
)

var BuildTarget string

// SysvScript Keep it in sync with the template from service_sysv_linux.go file
// Use "ps | grep -v grep | grep $(get_pid)" because "ps PID" may not work on OpenWrt
const SysvScript = `#!/bin/sh
# For RedHat and cousins:
# chkconfig: - 99 01
# description: {{.Description}}
# processname: {{.Path}}
### BEGIN INIT INFO
# Provides:          {{.Path}}
# Required-Start:
# Required-Stop:
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: {{.DisplayName}}
# Description:       {{.Description}}
### END INIT INFO
cmd="{{.Path}}{{range .Arguments}} {{.|cmd}}{{end}}"
name=$(basename $(readlink -f $0))
pid_file="/var/run/$name.pid"
stdout_log="/var/log/$name.log"
stderr_log="/var/log/$name.err"
[ -e /etc/sysconfig/$name ] && . /etc/sysconfig/$name
get_pid() {
    cat "$pid_file"
}
is_running() {
    [ -f "$pid_file" ] && ps | grep -v grep | grep $(get_pid) > /dev/null 2>&1
}
case "$1" in
    start)
        if is_running; then
            echo "Already started"
        else
            echo "Starting $name"
            {{if .WorkingDirectory}}cd '{{.WorkingDirectory}}'{{end}}
            $cmd >> "$stdout_log" 2>> "$stderr_log" &
            echo $! > "$pid_file"
            if ! is_running; then
                echo "Unable to start, see $stdout_log and $stderr_log"
                exit 1
            fi
        fi
    ;;
    stop)
        if is_running; then
            echo -n "Stopping $name.."
            kill $(get_pid)
            for i in $(seq 1 10)
            do
                if ! is_running; then
                    break
                fi
                echo -n "."
                sleep 1
            done
            echo
            if is_running; then
                echo "Not stopped; may still be shutting down or shutdown may have failed"
                exit 1
            else
                echo "Stopped"
                if [ -f "$pid_file" ]; then
                    rm "$pid_file"
                fi
            fi
        else
            echo "Not running"
        fi
    ;;
    restart)
        $0 stop
        if is_running; then
            echo "Unable to stop, will not attempt to start"
            exit 1
        fi
        $0 start
    ;;
    status)
        if is_running; then
            echo "Running"
        else
            echo "Stopped"
            exit 1
        fi
    ;;
    *)
    echo "Usage: $0 {start|stop|restart|status}"
    exit 1
    ;;
esac
exit 0
`

const SystemdScript = `[Unit]
Description={{.Description}}
ConditionFileIsExecutable={{.Path|cmdEscape}}
{{range $i, $dep := .Dependencies}}
{{$dep}} {{end}}
[Service]
LimitNOFILE=65536
StartLimitInterval=5
StartLimitBurst=10
ExecStart={{.Path|cmdEscape}}{{range .Arguments}} {{.|cmd}}{{end}}
{{if .ChRoot}}RootDirectory={{.ChRoot|cmd}}{{end}}
{{if .WorkingDirectory}}WorkingDirectory={{.WorkingDirectory|cmdEscape}}{{end}}
{{if .UserName}}User={{.UserName}}{{end}}
{{if .ReloadSignal}}ExecReload=/bin/kill -{{.ReloadSignal}} "$MAINPID"{{end}}
{{if .PIDFile}}PIDFile={{.PIDFile|cmd}}{{end}}
{{if and .LogOutput .HasOutputFileSupport -}}
StandardOutput=file:/var/log/{{.Name}}.out
StandardError=file:/var/log/{{.Name}}.err
{{- end}}
Restart=always
RestartSec=120
[Install]
WantedBy=multi-user.target
`

func UpdateNps() {
	destPath := downloadLatest("server")
	//复制文件到对应目录
	copyStaticFile(destPath, "nps")
	fmt.Println("Update completed, please restart")
}

func UpdateNpc() {
	destPath := downloadLatest("client")
	//复制文件到对应目录
	copyStaticFile(destPath, "npc")
	fmt.Println("Update completed, please restart")
}

type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Digest             string `json:"digest"`
	} `json:"assets"`
}

func downloadLatest(bin string) string {
	const timeout = 5 * time.Second
	const idleTimeout = 10 * time.Second
	const keepAliveTime = 30 * time.Second

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{
				Timeout:   timeout,
				KeepAlive: keepAliveTime,
			}
			raw, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return conn.NewTimeoutConn(raw, idleTimeout), nil
		},
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{
				Timeout:   timeout,
				KeepAlive: keepAliveTime,
			}
			raw, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			host, _, _ := net.SplitHostPort(addr)
			tlsConf := &tls.Config{InsecureSkipVerify: true}
			if net.ParseIP(host) == nil {
				tlsConf.ServerName = host
			}
			return conn.NewTimeoutTLSConn(raw, tlsConf, idleTimeout, timeout)
		},
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	httpClient := &http.Client{
		Transport: transport,
	}

	useCDNLatest := true
	rl := new(release)
	var version string
	// get version
	data, err := httpClient.Get("https://api.github.com/repos/djylb/nps/releases/latest")
	if err == nil {
		defer data.Body.Close()
		b, err := io.ReadAll(data.Body)
		if err == nil {
			if err := json.Unmarshal(b, &rl); err == nil {
				version = rl.TagName
				if version != "" {
					useCDNLatest = false
				}
				fmt.Println("The latest version is", version)
			}
		}
	}
	if useCDNLatest {
		version = "latest"
		fmt.Println("GitHub API failed; use CDN @latest (skip hash).")
	}

	osName := runtime.GOOS
	archName := runtime.GOARCH

	var filename string
	switch {
	case BuildTarget == "win7":
		filename = fmt.Sprintf("%s_%s_%s_old.tar.gz", osName, archName, bin)
	case BuildTarget != "":
		filename = fmt.Sprintf("%s_%s_%s_%s.tar.gz", osName, archName, BuildTarget, bin)
	default:
		filename = fmt.Sprintf("%s_%s_%s.tar.gz", osName, archName, bin)
	}

	var expectedHash string
	if !useCDNLatest {
		for _, a := range rl.Assets {
			if a.Name != filename {
				continue
			}
			//fmt.Println("Expected Hash:", a.Digest)
			if strings.HasPrefix(a.Digest, "sha256:") {
				expectedHash = strings.TrimPrefix(a.Digest, "sha256:")
			}
			break
		}
		//fmt.Println("Expected SHA256:", expectedHash)
		if expectedHash == "" {
			fmt.Println("No SHA256 digest found for", filename, "; skipping hash check")
		}
	} else {
		expectedHash = ""
	}

	// download latest package
	var urls []string
	if useCDNLatest {
		urls = []string{
			fmt.Sprintf("https://cdn.jsdelivr.net/gh/djylb/nps-mirror@latest/%s", filename),
			fmt.Sprintf("https://fastly.jsdelivr.net/gh/djylb/nps-mirror@latest/%s", filename),
			fmt.Sprintf("https://github.com/djylb/nps/releases/latest/download/%s", filename),
			fmt.Sprintf("https://gcore.jsdelivr.net/gh/djylb/nps-mirror@latest/%s", filename),
			fmt.Sprintf("https://testingcf.jsdelivr.net/gh/djylb/nps-mirror@latest/%s", filename),
		}
	} else {
		urls = []string{
			fmt.Sprintf("https://github.com/djylb/nps/releases/download/%s/%s", version, filename),
			fmt.Sprintf("https://cdn.jsdelivr.net/gh/djylb/nps-mirror@%s/%s", version, filename),
			fmt.Sprintf("https://fastly.jsdelivr.net/gh/djylb/nps-mirror@%s/%s", version, filename),
			fmt.Sprintf("https://gcore.jsdelivr.net/gh/djylb/nps-mirror@%s/%s", version, filename),
			fmt.Sprintf("https://testingcf.jsdelivr.net/gh/djylb/nps-mirror@%s/%s", version, filename),
		}
	}

	var lastErr error
	for _, url := range urls {
		fmt.Println("Trying:", url)
		resp, err := httpClient.Get(url)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
			_ = resp.Body.Close()
			continue
		}

		var reader io.Reader = resp.Body
		var hasher hash.Hash
		if expectedHash != "" {
			hasher = sha256.New()
			reader = io.TeeReader(resp.Body, hasher)
		}

		destPath, err := os.MkdirTemp(os.TempDir(), "nps-")
		if err != nil {
			_ = resp.Body.Close()
			lastErr = err
			//continue
			log.Fatal("Failed to create temp directory:", err)
		}

		if err := unpackit.Unpack(reader, destPath); err != nil {
			_ = resp.Body.Close()
			_ = os.RemoveAll(destPath)
			fmt.Println("  → failed:", err)
			lastErr = err
			continue
		}
		_ = resp.Body.Close()

		if expectedHash != "" {
			sum := hex.EncodeToString(hasher.Sum(nil))
			if sum != expectedHash {
				fmt.Printf("  → checksum mismatch: got %s vs %s\n", sum, expectedHash)
				_ = os.RemoveAll(destPath)
				lastErr = fmt.Errorf("checksum mismatch")
				continue
			}
			//fmt.Printf("  → checksum verified: %s\n", sum)
		}

		if bin == "server" {
			destPath = strings.Replace(destPath, "/web", "", -1)
			destPath = strings.Replace(destPath, `\web`, "", -1)
			destPath = strings.Replace(destPath, "/views", "", -1)
			destPath = strings.Replace(destPath, `\views`, "", -1)
		} else {
			destPath = strings.Replace(destPath, `\conf`, "", -1)
			destPath = strings.Replace(destPath, "/conf", "", -1)
		}
		return destPath
	}
	log.Fatalf("All mirrors failed; last error: %v", lastErr)
	return ""
}

func copyStaticFile(srcPath, bin string) string {
	path := common.GetInstallPath()
	if bin == "nps" {
		if err := CopyDir(filepath.Join(srcPath, "web", "views"), filepath.Join(path, "web", "views")); err != nil {
			if exists, _ := pathExists(filepath.Join(path, "web", "views")); exists {
				goto ExecPath
			}
			log.Fatalln(err)
		}
		chMod(filepath.Join(path, "web", "views"), 0766)
		if err := CopyDir(filepath.Join(srcPath, "web", "static"), filepath.Join(path, "web", "static")); err != nil {
			if exists, _ := pathExists(filepath.Join(path, "web", "static")); exists {
				goto ExecPath
			}
			log.Fatalln(err)
		}
		chMod(filepath.Join(path, "web", "static"), 0766)
		if _, err := copyFile(filepath.Join(srcPath, "conf", "nps.conf"), filepath.Join(path, "conf", "nps.conf.default")); err != nil {
			if exists, _ := pathExists(filepath.Join(path, "conf", "nps.conf")); exists {
				goto ExecPath
			}
			log.Fatalln(err)
		}
		chMod(filepath.Join(path, "conf", "nps.conf.default"), 0766)
	}
ExecPath:
	binPath, err := os.Executable()
	if err != nil {
		binPath, _ = filepath.Abs(os.Args[0])
	}

	if !common.IsWindows() {
		_, _ = copyFile(filepath.Join(srcPath, bin), binPath)
		chMod(binPath, 0755)
		if _, err := copyFile(filepath.Join(srcPath, bin), "/usr/bin/"+bin); err != nil {
			if _, err := copyFile(filepath.Join(srcPath, bin), "/usr/local/bin/"+bin); err != nil {
				log.Fatalln(err)
			} else {
				_, _ = copyFile(filepath.Join(srcPath, bin), "/usr/local/bin/"+bin+"-update")
				chMod("/usr/local/bin/"+bin+"-update", 0755)
				binPath = "/usr/local/bin/" + bin
			}
		} else {
			_, _ = copyFile(filepath.Join(srcPath, bin), "/usr/bin/"+bin+"-update")
			chMod("/usr/bin/"+bin+"-update", 0755)
			binPath = "/usr/bin/" + bin
		}
	} else {
		_, _ = copyFile(filepath.Join(srcPath, bin+".exe"), filepath.Join(common.GetAppPath(), bin+"-update.exe"))
		_, _ = copyFile(filepath.Join(srcPath, bin+".exe"), filepath.Join(common.GetAppPath(), bin+".exe"))
	}
	chMod(binPath, 0755)
	return binPath
}

func InstallNpc() {
	path := common.GetInstallPath()
	if !common.FileExists(path) {
		err := os.MkdirAll(path, 0755)
		if err != nil {
			log.Fatal(err)
		}
	}
	copyStaticFile(common.GetAppPath(), "npc")
}

func InstallNps() string {
	path := common.GetInstallPath()
	log.Println("install path:" + path)
	if common.FileExists(path) {
		MkidrDirAll(path, "web/static", "web/views")
	} else {
		MkidrDirAll(path, "conf", "web/static", "web/views")
		// not copy config if the config file is exist
		if err := CopyDir(filepath.Join(common.GetAppPath(), "conf"), filepath.Join(path, "conf")); err != nil {
			log.Fatalln(err)
		}
		chMod(filepath.Join(path, "conf"), 0766)
	}
	binPath := copyStaticFile(common.GetAppPath(), "nps")
	log.Println("install ok!")
	log.Println("Static files and configuration files in the current directory will be useless")
	log.Println("The new configuration file is located in", path, "you can edit them")
	if !common.IsWindows() {
		log.Println(`You can start with:
nps start|stop|restart|uninstall|update or nps-update update
anywhere!`)
	} else {
		log.Println(`You can copy executable files to any directory and start working with:
nps.exe start|stop|restart|uninstall|update or nps-update.exe update
now!`)
	}
	chMod(common.GetLogPath(), 0777)
	return binPath
}

func MkidrDirAll(path string, v ...string) {
	for _, item := range v {
		if err := os.MkdirAll(filepath.Join(path, item), 0755); err != nil {
			log.Fatalf("Failed to create directory %s error:%s", path, err.Error())
		}
	}
}

func CopyDir(srcPath string, destPath string) error {
	//检测目录正确性
	if srcInfo, err := os.Stat(srcPath); err != nil {
		//fmt.Println(err.Error())
		log.Println("Failed to copy source directory.")
		return err
	} else {
		if !srcInfo.IsDir() {
			return errors.New("srcPath is not a directory")
		}
	}
	if destInfo, err := os.Stat(destPath); err != nil {
		if os.IsNotExist(err) {
			if mkErr := os.MkdirAll(destPath, os.ModePerm); mkErr != nil {
				return mkErr
			}
		} else {
			return err
		}
	} else {
		if !destInfo.IsDir() {
			return errors.New("destInfo is not the right directory")
		}
	}
	err := filepath.Walk(srcPath, func(path string, f os.FileInfo, err error) error {
		if f == nil {
			return err
		}
		if !f.IsDir() {
			destNewPath := strings.Replace(path, srcPath, destPath, -1)
			log.Println("copy file: " + path + " -> " + destNewPath)
			_, _ = copyFile(path, destNewPath)
			if !common.IsWindows() {
				chMod(destNewPath, 0766)
			}
		}
		return nil
	})
	return err
}

// 生成目录并拷贝文件
func copyFile(src, dest string) (w int64, err error) {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return 0, err
	}
	destAbs, err := filepath.Abs(dest)
	if err != nil {
		return 0, err
	}
	if srcAbs == destAbs {
		return 0, nil
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return
	}
	defer srcFile.Close()

	// 确保目录存在
	dirPath := filepath.Dir(dest)
	if exists, _ := pathExists(dirPath); !exists {
		log.Println("mkdir all:", dirPath)
		if err := os.MkdirAll(dirPath, os.ModePerm); err != nil {
			log.Fatalln(err)
		}
	}

	dstFile, err := os.Create(dest)
	if err == nil {
		defer dstFile.Close()
		if n, copyErr := io.Copy(dstFile, srcFile); copyErr == nil {
			return n, nil
		}
	}

	tmpPath := dest + ".tmp"
	if _, statErr := os.Stat(tmpPath); statErr == nil {
		_ = os.Remove(tmpPath)
	}

	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return 0, err
	}
	defer tmpFile.Close()

	if _, err = srcFile.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := io.Copy(tmpFile, srcFile)
	if err != nil {
		return n, err
	}

	_ = tmpFile.Close()
	if renameErr := os.Rename(tmpPath, dest); renameErr != nil {
		return n, renameErr
	}
	return n, nil
}

// 检测文件夹路径是否存在
func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func chMod(name string, mode os.FileMode) {
	if !common.IsWindows() {
		_ = os.Chmod(name, mode)
	}
}
