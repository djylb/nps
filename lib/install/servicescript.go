package install

import (
	"os"
	"strings"

	"github.com/kardianos/service"
)

// The service library writes the command line of a service into its
// service files without quoting it for them: System V and procd put
// {{. | cmd}} ("..." with only " escaped) inside cmd="...", whose
// unquoted expansion then splits the words again, OpenRC joins the
// arguments into command_args unquoted, upstart quotes as System V does,
// and systemd leaves % specifiers and $ variables in them. An argument
// with a space, a quote, |, &, ;, $ or % then does not reach the program
// as given. SetServiceScripts replaces those templates with the library's
// own, except that the command line is written in, quoted for the file,
// by this package. The library's mini template language has no escape for
// "{{", so the quoting never leaves one in the command line.

// SetServiceScripts sets the service file template of the init system of
// this host in c.Option, with c.Executable and c.Arguments written in as
// one quoted command line. Call it after both are final, right before the
// service is installed.
func SetServiceScripts(c *service.Config) {
	if c.Option == nil {
		c.Option = make(service.KeyValue)
	}
	path := c.Executable
	if path == "" {
		path, _ = os.Executable()
	}
	key, script := serviceScript(host.platform(), path, c.Arguments)
	if key != "" {
		c.Option[key] = script
	}
}

// serviceScript returns the option key and the template of the init system
// platform, for the program path run with args; "" when the library's
// template quotes correctly or is not used.
func serviceScript(platform, path string, args []string) (key, script string) {
	switch platform {
	case "linux-systemd":
		return "SystemdScript", strings.Replace(systemdScript, "@EXEC@", systemdCommand(path, args), 1)
	case "unix-systemv":
		return "SysvScript", strings.NewReplacer("@PATH@", shQuote(path), "@EXEC@", shCommand(path, args)).Replace(sysvScript)
	case "linux-procd":
		return "SysvScript", strings.NewReplacer("@PATH@", shQuote(path), "@EXEC@", shCommand(path, args)).Replace(procdScript)
	case "linux-openrc":
		return "OpenRCScript", strings.NewReplacer("@PATH@", shQuote(path), "@ARGS@", openrcArgs(args)).Replace(openrcScript)
	case "linux-upstart":
		return "UpstartScript", strings.Replace(upstartScript, "@EXEC@", shCommand(path, args), 1)
	}
	return "", ""
}

// shQuote quotes s as one shell word: '...', with ' written as '\”. A "{{"
// is split as {”{, which the shell reads as {{ but the service library's
// template language does not take for an action.
func shQuote(s string) string {
	q := "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	for strings.Contains(q, "{{") {
		q = strings.ReplaceAll(q, "{{", "{''{")
	}
	return q
}

// shCommand is path and args as shell words.
func shCommand(path string, args []string) string {
	words := []string{shQuote(path)}
	for _, a := range args {
		words = append(words, shQuote(a))
	}
	return strings.Join(words, " ")
}

// openrcArgs is the command_args assignment value: OpenRC hands
// command_args to eval, so it holds the arguments as shell words, and the
// assignment quotes those words once more.
func openrcArgs(args []string) string {
	words := make([]string, len(args))
	for i, a := range args {
		words[i] = shQuote(a)
	}
	return shQuote(strings.Join(words, " "))
}

// systemdQuote quotes s as one word of a systemd command line: "...", with
// \ and " escaped, % written %% (no specifier) and $ written $$ (no
// variable). A "{" is written \x7b, which systemd reads as "{".
func systemdQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$", "{", `\x7b`).Replace(s) + `"`
}

// systemdCommand is the ExecStart value of path and args.
func systemdCommand(path string, args []string) string {
	words := []string{systemdQuote(path)}
	for _, a := range args {
		words = append(words, systemdQuote(a))
	}
	return strings.Join(words, " ")
}

// The templates below are those of github.com/kardianos/service v1.3.0
// with the command line replaced: @EXEC@ is the whole command line, @PATH@
// the quoted program and @ARGS@ the OpenRC arguments.

const systemdScript = `[Unit]
Description={{Description}}
ConditionFileIsExecutable={{Path | cmdEscape}}
{{range Dependencies}}{{.}}
{{end}}
[Service]
StartLimitInterval=5
StartLimitBurst=10
ExecStart=@EXEC@
{{if ChRoot}}RootDirectory={{ChRoot | cmd}}
{{end}}{{if WorkingDirectory}}WorkingDirectory={{WorkingDirectory | cmdEscape}}
{{end}}{{if UserName}}User={{UserName}}
{{end}}{{if ReloadSignal}}ExecReload=/bin/kill -{{ReloadSignal}} "$MAINPID"
{{end}}{{if PIDFile}}PIDFile={{PIDFile | cmd}}
{{end}}{{if OutputFileSupport}}StandardOutput=file:{{LogDirectory}}/{{Name}}.out
StandardError=file:{{LogDirectory}}/{{Name}}.err
{{end}}{{if LimitNOFILE}}LimitNOFILE={{LimitNOFILE}}
{{end}}{{if Restart}}Restart={{Restart}}
{{end}}{{if SuccessExitStatus}}SuccessExitStatus={{SuccessExitStatus}}
{{end}}RestartSec=120
EnvironmentFile=-/etc/sysconfig/{{Name}}

{{range EnvVars}}{{.}}
{{end}}[Install]
WantedBy=multi-user.target
`

// sysvScript runs the command through run_cmd, which execs it, so that $!
// is the PID of the program.
const sysvScript = `#!/bin/sh
# For RedHat and cousins:
# chkconfig: - 99 01
# description: {{Description}}
# processname: {{Path}}

### BEGIN INIT INFO
# Provides:          {{Path}}
# Required-Start:
# Required-Stop:
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: {{DisplayName}}
# Description:       {{Description}}
### END INIT INFO

cmd=@PATH@
run_cmd() {
    exec @EXEC@
}

name=$(basename $(readlink -f $0))
pid_file="/var/run/$name.pid"
stdout_log="{{LogDirectory}}/$name.log"
stderr_log="{{LogDirectory}}/$name.err"

{{range EnvVars}}{{.}}
{{end}}
[ -e /etc/sysconfig/$name ] && . /etc/sysconfig/$name

get_pid() {
    cat "$pid_file"
}

is_running() {
    [ -f "$pid_file" ] && cat /proc/$(get_pid)/stat > /dev/null 2>&1
}

case "$1" in
    start)
        if is_running; then
            echo "Already started"
        else
            echo "Starting $name"
            {{if WorkingDirectory}}cd '{{WorkingDirectory}}'{{end}}
            run_cmd >> "$stdout_log" 2>> "$stderr_log" &
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

const procdScript = `#!/bin/sh /etc/rc.common
USE_PROCD=1
# After network starts
START=21
# Before network stops
STOP=89
cmd=@PATH@
name="{{Name}}"
pid_file="/var/run/${name}.pid"

start_service() {
    echo "Starting ${name}"
    procd_open_instance
    procd_set_param command @EXEC@

    # respawn automatically if something died, be careful if you have an alternative process supervisor
    # if process exits sooner than respawn_threshold, it is considered crashed and after 5 retries the service is stopped
    # if process finishes later than respawn_threshold, it is restarted unconditionally, regardless of error code
    # notice that this is literal respawning of the process, no in a respawn-on-failure sense
    procd_set_param respawn ${respawn_threshold:-3600} ${respawn_timeout:-5} ${respawn_retry:-5}

    procd_set_param stdout 1             # forward stdout of the command to logd
    procd_set_param stderr 1             # same for stderr
    procd_set_param pidfile ${pid_file}  # write a pid file on instance start and remove it on stop
    procd_close_instance
    echo "${name} has been started"
}
`

const openrcScript = `#!/sbin/openrc-run
supervisor=supervise-daemon
name="{{DisplayName}}"
description="{{Description}}"
command=@PATH@
command_args=@ARGS@
name=$(basename $(readlink -f $command))
supervise_daemon_args="--stdout {{LogDirectory}}/${name}.log --stderr {{LogDirectory}}/${name}.err"

{{range EnvVars}}{{.}}
{{end}}
{{if Dependencies}}depend() {
{{range Dependencies}}{{.}}
{{end}}}
{{end}}`

const upstartScript = `# {{Description}}

{{if DisplayName}}description    "{{DisplayName}}"{{end}}

{{if KillStanza}}kill signal INT{{end}}
{{if ChRoot}}chroot {{ChRoot}}{{end}}
{{if WorkingDirectory}}chdir {{WorkingDirectory}}{{end}}
start on filesystem or runlevel [2345]
stop on runlevel [!2345]

{{SetUID}}

respawn
respawn limit 10 5
umask 022

console none

pre-start script
    test -x {{Path}} || { stop; exit 0; }
end script

# Start
script
	{{if LogOutput}}
	stdout_log="{{LogDirectory}}/{{Name}}.out"
	stderr_log="{{LogDirectory}}/{{Name}}.err"
	{{end}}

	if [ -f "/etc/sysconfig/{{Name}}" ]; then
		set -a
		source /etc/sysconfig/{{Name}}
		set +a
	fi

	exec {{SudoPrefix}}@EXEC@{{if LogOutput}} >> $stdout_log 2>> $stderr_log{{end}}
end script
`
