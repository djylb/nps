package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/djylb/nps/lib/runenv"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/httpproxy"
	"github.com/djylb/slogx"
)

// openAccessLog opens access_log, the file of every request to the domain
// proxy, rotated as the main log is. The format is combined (as web servers
// write it, for access log analysers) or json.
func openAccessLog(c servercfg.LogConfig) (func(httpproxy.AccessRecord), func(), error) {
	none := func() {}
	if c.Access == "" {
		return nil, none, nil
	}
	encode := combinedLine
	switch strings.ToLower(c.AccessFormat) {
	case "", "combined":
	case "json":
		encode = jsonAccessLine
	default:
		return nil, none, fmt.Errorf("unknown access_log_format %q (combined, json)", c.AccessFormat)
	}
	path := c.Access
	if !filepath.IsAbs(path) {
		path = filepath.Join(runenv.GetRunPath(), path)
	}
	size := int64(c.MaxSize) << 20
	if c.MaxSize <= 0 {
		size = -1
	}
	out, err := slogx.OpenRotating(slogx.Rotation{Path: path, MaxSize: size, MaxFiles: c.MaxFiles,
		MaxAge: time.Duration(c.MaxDays) * 24 * time.Hour, Compress: c.Compress})
	if err != nil {
		return nil, none, err
	}
	return func(r httpproxy.AccessRecord) { _ = out.Write(encode(r)) }, func() { _ = out.Close() }, nil
}

// combinedLine is the combined log format, with the duration in
// microseconds and the host entry appended (as %D and a custom field):
//
//	ip - - [08/Nov/2026:03:12:45 +0000] "GET /a?b HTTP/1.1" 200 512 "ref" "ua" 1530 "example.com"
func combinedLine(r httpproxy.AccessRecord) []byte {
	b := make([]byte, 0, 256)
	b = append(b, dash(r.SrcIP)...)
	b = append(b, " - - ["...)
	b = r.Time.UTC().AppendFormat(b, "02/Jan/2006:15:04:05 -0700")
	b = append(b, "] \""...)
	req := r.Method + " " + r.Path
	if r.Query != "" {
		req += "?" + r.Query
	}
	b = appendEscaped(b, req+" "+r.Proto)
	b = append(b, "\" "...)
	b = strconv.AppendInt(b, int64(r.Status), 10)
	b = append(b, ' ')
	if r.Bytes > 0 {
		b = strconv.AppendInt(b, r.Bytes, 10)
	} else {
		b = append(b, '-')
	}
	b = append(b, " \""...)
	b = appendEscaped(b, dash(r.Referer))
	b = append(b, "\" \""...)
	b = appendEscaped(b, dash(r.UserAgent))
	b = append(b, "\" "...)
	b = strconv.AppendInt(b, r.Duration.Microseconds(), 10)
	b = append(b, " \""...)
	b = appendEscaped(b, dash(r.Domain))
	return append(b, "\"\n"...)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// appendEscaped writes s as web servers do inside quotes: quotes,
// backslashes, control bytes and invalid UTF-8 as \xHH, so no request can
// break the line or its fields.
func appendEscaped(b []byte, s string) []byte {
	const hex = "0123456789ABCDEF"
	for i := 0; i < len(s); {
		c := s[i]
		if c >= utf8.RuneSelf {
			r, n := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && n == 1 || r >= 0x80 && r <= 0x9f {
				for j := i; j < i+n; j++ {
					b = append(b, '\\', 'x', hex[s[j]>>4], hex[s[j]&15])
				}
			} else {
				b = append(b, s[i:i+n]...)
			}
			i += n
			continue
		}
		if c < ' ' || c == 0x7f || c == '"' || c == '\\' {
			b = append(b, '\\', 'x', hex[c>>4], hex[c&15])
		} else {
			b = append(b, c)
		}
		i++
	}
	return b
}

// jsonAccessLine is one JSON object per request, with the field names of
// the main log.
func jsonAccessLine(r httpproxy.AccessRecord) []byte {
	b, _ := json.Marshal(map[string]any{
		"time": r.Time.UTC().Format("2006-01-02T15:04:05.000Z07:00"), "route": r.Route, "host": r.Host,
		"src_ip": r.SrcIP, "method": r.Method, "domain": r.Domain, "path": r.Path, "query": r.Query,
		"proto": r.Proto, "status": r.Status, "bytes": r.Bytes, "dur_ms": r.Duration.Milliseconds(),
		"target": r.Target, "referer": r.Referer, "user_agent": r.UserAgent,
	})
	return append(b, '\n')
}
