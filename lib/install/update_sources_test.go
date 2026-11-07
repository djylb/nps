package install

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/djylb/selfupdate"
)

func TestMirrorSourceMapsReleaseURLToTag(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.URL.Path }))
	defer srv.Close()
	s := &mirrorSource{selfupdate.HTTPSource{URL: srv.URL + "/gh/nps@manifest-channel/manifest.json", Base: srv.URL + "/gh/"}}
	r, _, err := s.Open(context.Background(), selfupdate.Artifact{URL: githubReleases + "v0.35.1/a.tar.gz"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if got != "/gh/nps-mirror@v0.35.1/a.tar.gz" {
		t.Fatalf("requested %q", got)
	}
}

func TestMirrorSourceKeepsOtherURLs(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.URL.Path }))
	defer srv.Close()
	s := &mirrorSource{selfupdate.HTTPSource{URL: srv.URL + "/gh/nps@manifest-channel/manifest.json", Base: srv.URL + "/gh/"}}
	r, _, err := s.Open(context.Background(), selfupdate.Artifact{URL: "files/a.tar.gz"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if got != "/gh/files/a.tar.gz" {
		t.Fatalf("requested %q", got)
	}
}

// TestUpdateSourcesLocations pins where every released version looks for
// updates; changing it strands the versions already installed.
func TestUpdateSourcesLocations(t *testing.T) {
	want := []string{
		"https://github.com/djylb/nps/releases/download/channel/manifest.json|",
		"https://cdn.jsdelivr.net/gh/djylb/nps@manifest-channel/manifest.json|https://cdn.jsdelivr.net/gh/djylb/",
		"https://fastly.jsdelivr.net/gh/djylb/nps@manifest-channel/manifest.json|https://fastly.jsdelivr.net/gh/djylb/",
		"https://gcore.jsdelivr.net/gh/djylb/nps@manifest-channel/manifest.json|https://gcore.jsdelivr.net/gh/djylb/",
		"https://testingcf.jsdelivr.net/gh/djylb/nps@manifest-channel/manifest.json|https://testingcf.jsdelivr.net/gh/djylb/",
	}
	var got []string
	for i, s := range updateSources(http.DefaultClient) {
		var h *selfupdate.HTTPSource
		switch s := s.(type) {
		case *selfupdate.HTTPSource:
			if i != 0 {
				t.Fatalf("source %d is not a mirror", i)
			}
			h = s
		case *mirrorSource:
			h = &s.HTTPSource
		default:
			t.Fatalf("source %d is %T", i, s)
		}
		got = append(got, h.URL+"|"+h.Base)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sources:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
