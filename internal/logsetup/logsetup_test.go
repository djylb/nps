package logsetup

import (
	"strings"
	"testing"

	"github.com/djylb/slogx"
)

func TestSecretsSwitch(t *testing.T) {
	t.Cleanup(func() { _ = slogx.Default.Configure(slogx.Config{}) })
	line := func() string {
		slogx.Module("t").Info("x", "vkey", slogx.Redacted("k3y"))
		var b []byte
		for _, e := range slogx.Default.Memory().Take() {
			r := e.Record()
			b = slogx.TextEncoder{}.Encode(b, &r)
		}
		return string(b)
	}
	if err := Apply(Options{Output: "off", Memory: 100}); err != nil {
		t.Fatal(err)
	}
	if s := line(); strings.Contains(s, "k3y") {
		t.Fatalf("secret shown by default: %s", s)
	}
	if err := Apply(Options{Output: "off", Memory: 100, Secrets: true}); err != nil {
		t.Fatal(err)
	}
	if s := line(); !strings.Contains(s, "vkey=k3y") {
		t.Fatalf("log_secrets did not show it: %s", s)
	}
	slogx.Default.SetLogSecrets(false)
	if s := line(); strings.Contains(s, "k3y") {
		t.Fatalf("switched off but shown: %s", s)
	}
}

func TestInstanceOnEveryLine(t *testing.T) {
	t.Cleanup(func() { _ = slogx.Default.Configure(slogx.Config{}) })
	if err := Apply(Options{Output: "off", Memory: 10, Instance: "sg1"}); err != nil {
		t.Fatal(err)
	}
	slogx.Module("t").Info("x", "k", 1)
	es := slogx.Default.Memory().Take()
	r := es[len(es)-1].Record()
	b := slogx.TextEncoder{}.Encode(nil, &r)
	if !strings.Contains(string(b), "k=1 instance=sg1 msg=x") {
		t.Fatalf("got %s", b)
	}
}
