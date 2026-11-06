package install

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestUpdateSourcesReadTheFixedManifest(t *testing.T) {
	first := updateSources(http.DefaultClient)[0].(*selfupdate.HTTPSource)
	if first.URL != githubReleases+"channel/manifest.json" {
		t.Fatal(first.URL)
	}
}
