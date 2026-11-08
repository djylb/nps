//go:build !sdk

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	flag "github.com/spf13/pflag"
)

// The service definition may be readable by other users and is not where a
// later npc looks for connection settings, so an installed service keeps no
// vkey on its command line: the connection settings go into files under the
// install directory, written for the owner only, and the service runs them
// with --launch @file.
const (
	serviceConnFile   = "npc-service.json"
	serviceLaunchFile = "npc-service-launch-%d.conf"
)

// connFlags are the flags a connection from the command line is made of,
// with the launch payload key each one becomes.
var connFlags = map[string]string{
	"server": "server", "vkey": "vkey", "type": "type", "proxy": "proxy", "local_ip": "local_ip",
}

// localFlags make the local server of -password, with its npc.conf key.
var localFlags = map[string]string{
	"local_type": "local_type", "local_port": "local_port", "password": "password",
	"target": "target_addr", "target_type": "target_type",
	"local_proxy": "local_proxy", "fallback_secret": "fallback_secret",
}

// serviceOnly are left out of the service arguments: they choose what runs
// or are replaced by the files written.
var serviceOnly = map[string]bool{"launch": true, "config": true, "service": true, "debug": true}

type serviceFile struct {
	path string
	data []byte
}

// serviceArguments returns the arguments of the npc service installed from
// the parsed flags fs and the files to write first. Connection settings go
// into files in confDir; launchEnv is NPC_LAUNCH, used when no --launch is
// given. Relative paths are made absolute against the current directory.
func serviceArguments(fs *flag.FlagSet, confDir, launchEnv string) ([]string, []serviceFile, error) {
	var (
		args, inputs []string
		files        []serviceFile
	)
	changed := func(name string) (*flag.Flag, bool) {
		f := fs.Lookup(name)
		return f, f != nil && f.Changed
	}
	addInput := func(input string) error {
		input = strings.TrimSpace(input)
		if input == "" {
			return nil
		}
		if input[0] == '@' {
			path, err := filepath.Abs(strings.TrimSpace(input[1:]))
			if err != nil {
				return err
			}
			inputs = append(inputs, "@"+path)
			return nil
		}
		path := filepath.Join(confDir, fmt.Sprintf(serviceLaunchFile, len(files)+1))
		files = append(files, serviceFile{path, []byte(input + "\n")})
		inputs = append(inputs, "@"+path)
		return nil
	}

	var launchInputs []string
	if f, ok := changed("launch"); ok {
		launchInputs, _ = fs.GetStringArray(f.Name)
	} else if strings.TrimSpace(launchEnv) != "" {
		launchInputs = []string{launchEnv}
	}
	for _, in := range launchInputs {
		if err := addInput(in); err != nil {
			return nil, nil, err
		}
	}

	server, _ := fs.GetString("server")
	vkey, _ := fs.GetString("vkey")
	hasCommand, err := commandLineMode(server, vkey)
	if err != nil {
		return nil, nil, err
	}
	_, p2p := changed("password")
	launchMode := len(inputs) > 0
	if !launchMode && hasCommand {
		payload := make(map[string]any)
		for name, key := range connFlags {
			if f, ok := changed(name); ok {
				payload[key] = f.Value.String()
			}
		}
		if p2p {
			local := make(map[string]any)
			for name, key := range localFlags {
				if f, ok := changed(name); ok {
					local[key] = f.Value.String()
				}
			}
			payload["locals"] = []any{local}
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return nil, nil, err
		}
		path := filepath.Join(confDir, serviceConnFile)
		files = append([]serviceFile{{path, append(data, '\n')}}, files...)
		inputs = append(inputs, "@"+path)
		// -config runs beside the command line connection but --launch
		// replaces both, so the config files become launch inputs too.
		if !p2p {
			if f, ok := changed("config"); ok {
				for p := range strings.SplitSeq(f.Value.String(), ",") {
					if p = strings.TrimSpace(p); p != "" {
						if err := addInput("@" + p); err != nil {
							return nil, nil, err
						}
					}
				}
			}
		}
	}

	for _, in := range inputs {
		args = append(args, "--launch="+in)
	}
	if f, ok := changed("config"); ok && len(inputs) == 0 {
		paths := strings.Split(f.Value.String(), ",")
		for i, p := range paths {
			if p = strings.TrimSpace(p); p != "" {
				if paths[i], err = filepath.Abs(p); err != nil {
					return nil, nil, err
				}
			}
		}
		args = append(args, "--config="+strings.Join(paths, ","))
	}
	var flagErr error
	fs.Visit(func(f *flag.Flag) {
		name := f.Name
		if serviceOnly[name] || (launchMode || hasCommand) && (connFlags[name] != "" || localFlags[name] != "") {
			return
		}
		if _, ok := f.Value.(flag.SliceValue); ok {
			flagErr = errors.Join(flagErr, fmt.Errorf("--%s cannot be kept in the service arguments", name))
			return
		}
		args = append(args, "--"+name+"="+f.Value.String())
	})
	if flagErr != nil {
		return nil, nil, flagErr
	}
	return append(args, "-debug=false"), files, nil
}

// writeServiceFiles writes files readable by the owner only and removes
// launch files of an earlier install that are no longer used.
func writeServiceFiles(confDir string, files []serviceFile) error {
	keep := make(map[string]bool)
	for _, f := range files {
		keep[f.path] = true
	}
	old, _ := filepath.Glob(filepath.Join(confDir, strings.Replace(serviceLaunchFile, "%d", "*", 1)))
	if p := filepath.Join(confDir, serviceConnFile); !keep[p] {
		old = append(old, p)
	}
	for _, p := range old {
		if !keep[p] {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	if len(files) == 0 {
		return nil
	}
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		return err
	}
	for _, f := range files {
		if err := writePrivate(f.path, f.data); err != nil {
			return err
		}
	}
	return nil
}

// writePrivate replaces path with data, readable by the owner only also when
// the file existed with wider permissions.
func writePrivate(path string, data []byte) error {
	tmp := path + ".tmp" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
