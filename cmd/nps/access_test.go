package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/server/httpproxy"
)

func TestAccessLines(t *testing.T) {
	r := httpproxy.AccessRecord{Time: time.Date(2026, 11, 8, 3, 12, 45, 0, time.UTC), SrcIP: "192.0.2.1", Method: "GET",
		Path: "/a\"b", Query: "x=1", Proto: "HTTP/1.1", Status: 200, Bytes: 512, Duration: 1530 * time.Microsecond,
		Referer: "", UserAgent: "ua\nevil", Domain: "example.com"}
	got := string(combinedLine(r))
	want := `192.0.2.1 - - [08/Nov/2026:03:12:45 +0000] "GET /a\x22b?x=1 HTTP/1.1" 200 512 "-" "ua\x0Aevil" 1530 "example.com"` + "\n"
	if got != want {
		t.Fatalf("combined:\n%s\nwant\n%s", got, want)
	}
	var m map[string]any
	if err := json.Unmarshal(jsonAccessLine(r), &m); err != nil || m["status"] != float64(200) || m["dur_ms"] != float64(1) {
		t.Fatalf("json %v %v", m, err)
	}
	if strings.Count(got, "\n") != 1 {
		t.Fatal("line broken")
	}
}
