// Package logsetup turns the log settings of nps and npc into obs
// configuration, the same way for both.
package logsetup

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/djylb/nps/lib/obs"
	_ "github.com/djylb/nps/lib/logevent" // registers the events
)

// Options are the log settings as nps.conf and the npc flags give them.
type Options struct {
	Output   string // stdout, file, both, off
	Format   string // text, json
	Level    string
	Path     string // file; relative to Dir
	Security string // security log file; relative to Dir
	Dir      string
	MaxSize  int // MB
	MaxFiles int
	MaxDays  int
	Compress bool
	// Service: a service has no console, so stdout means the file.
	Service bool
	// Buffer keeps the last bytes of lines in memory (npc SDK).
	Buffer int
}

// Config returns the obs configuration of o.
func (o Options) Config() (obs.Config, error) {
	out := strings.ToLower(strings.TrimSpace(o.Output))
	switch out {
	case "", "stdout":
		out = "stdout"
	case "false":
		out = "off"
	case "file", "both", "off":
	default:
		return obs.Config{}, fmt.Errorf("unknown log output %q (stdout, file, both, off)", o.Output)
	}
	path := o.Path
	switch strings.ToLower(path) {
	case "off", "false", "docker", "/dev/null":
		path = ""
	}
	if path == "" && (out == "file" || out == "both") {
		out = "stdout"
	}
	if o.Service && out == "stdout" && path != "" {
		out = "file"
	}
	c := obs.Config{Output: out, Level: o.Level, Buffer: o.Buffer}
	switch strings.ToLower(o.Format) {
	case "", "text", "logfmt":
	case "json":
		c.Format = obs.JSON
	default:
		return obs.Config{}, fmt.Errorf("unknown log format %q (text, json)", o.Format)
	}
	rot := func(p string) obs.Rotation {
		if p != "" && !filepath.IsAbs(p) {
			p = filepath.Join(o.Dir, p)
		}
		size := int64(o.MaxSize) << 20
		if o.MaxSize <= 0 {
			size = -1
		}
		return obs.Rotation{Path: p, MaxSize: size, MaxFiles: o.MaxFiles,
			MaxAge: time.Duration(o.MaxDays) * 24 * time.Hour, Compress: o.Compress}
	}
	if out == "file" || out == "both" {
		c.File = rot(path)
	}
	if o.Security != "" {
		c.SecurityFile = rot(o.Security)
	}
	return c, nil
}

// Apply configures obs.Default as o says and sends the standard library's
// logger to it.
func Apply(o Options) error {
	c, err := o.Config()
	if err != nil {
		return err
	}
	if err := obs.Default.Configure(c); err != nil {
		return err
	}
	obs.CaptureStdLog(obs.Module("lib"))
	return nil
}
