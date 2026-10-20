package service

import (
	"errors"
	"testing"

	"github.com/djylb/nps/lib/file"
)

type rateLimitRepository struct {
	stubRepository
	tunnel                 *file.Tunnel
	host                   *file.Host
	tunnelSaves, hostSaves int
}

func (r *rateLimitRepository) GetTunnel(int) (*file.Tunnel, error) {
	if r.tunnel == nil {
		return nil, ErrTunnelNotFound
	}
	return r.tunnel, nil
}
func (r *rateLimitRepository) GetHost(int) (*file.Host, error) {
	if r.host == nil {
		return nil, ErrHostNotFound
	}
	return r.host, nil
}
func (r *rateLimitRepository) CreateTunnel(v *file.Tunnel) error { r.tunnel = v; return nil }
func (r *rateLimitRepository) CreateHost(v *file.Host) error     { r.host = v; return nil }
func (r *rateLimitRepository) SaveTunnel(v *file.Tunnel) error {
	r.tunnel = v
	r.tunnelSaves++
	return nil
}
func (r *rateLimitRepository) SaveHost(v *file.Host, _ string) error {
	r.host = v
	r.hostSaves++
	return nil
}

func newRateLimitService() (DefaultIndexService, *rateLimitRepository) {
	r := &rateLimitRepository{stubRepository: stubRepository{getClient: func(int) (*file.Client, error) { return &file.Client{Id: 1}, nil }}}
	return DefaultIndexService{Backend: Backend{Repository: r, Runtime: stubRuntime{}}}, r
}

func TestIndexRateLimitsCreateEditClear(t *testing.T) {
	s, r := newRateLimitService()
	if _, err := s.AddTunnel(AddTunnelInput{ClientID: 1, Mode: "tcp", Port: 1234, UploadLimit: 12, DownloadLimit: 34}); err != nil {
		t.Fatal(err)
	}
	if r.tunnel.UploadLimit != 12 || r.tunnel.DownloadLimit != 34 {
		t.Fatalf("created tunnel limits: %+v", r.tunnel)
	}
	if _, err := s.AddHost(AddHostInput{ClientID: 1, Host: "example.com", UploadLimit: 56, DownloadLimit: 78}); err != nil {
		t.Fatal(err)
	}
	if r.host.UploadLimit != 56 || r.host.DownloadLimit != 78 {
		t.Fatalf("created host limits: %+v", r.host)
	}
	if _, err := s.EditTunnel(EditTunnelInput{UploadLimit: -1, DownloadLimit: 90}); err != nil {
		t.Fatal(err)
	}
	if r.tunnel.UploadLimit != 0 || r.tunnel.DownloadLimit != 90 {
		t.Fatalf("edited tunnel limits: %+v", r.tunnel)
	}
	if _, err := s.EditHost(EditHostInput{Host: "example.com", UploadLimit: 91, DownloadLimit: -2}); err != nil {
		t.Fatal(err)
	}
	if r.host.UploadLimit != 91 || r.host.DownloadLimit != 0 {
		t.Fatalf("edited host limits: %+v", r.host)
	}
	for _, mode := range []string{"upload_limit", "download_limit"} {
		r.tunnel.SetRateLimits(100, 200)
		r.host.SetRateLimits(300, 400)
		ts, hs := r.tunnelSaves, r.hostSaves
		if err := s.ClearTunnel(0, mode); err != nil {
			t.Fatal(err)
		}
		if err := s.ClearHost(0, mode); err != nil {
			t.Fatal(err)
		}
		if r.tunnelSaves != ts+1 || r.hostSaves != hs+1 {
			t.Fatal("clear did not persist")
		}
		if mode == "upload_limit" {
			if r.tunnel.UploadLimit != 0 || r.tunnel.DownloadLimit != 200 || r.host.UploadLimit != 0 || r.host.DownloadLimit != 400 {
				t.Fatal("upload clear changed other direction")
			}
		} else if r.tunnel.UploadLimit != 100 || r.tunnel.DownloadLimit != 0 || r.host.UploadLimit != 300 || r.host.DownloadLimit != 0 {
			t.Fatal("download clear changed other direction")
		}
	}
}

func TestIndexRateLimitNegativeCreation(t *testing.T) {
	s, r := newRateLimitService()
	if _, err := s.AddTunnel(AddTunnelInput{ClientID: 1, Mode: "tcp", Port: 1234, UploadLimit: -1, DownloadLimit: -2}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddHost(AddHostInput{ClientID: 1, Host: "example.com", UploadLimit: -1, DownloadLimit: -2}); err != nil {
		t.Fatal(err)
	}
	if r.tunnel.UploadLimit != 0 || r.tunnel.DownloadLimit != 0 || r.host.UploadLimit != 0 || r.host.DownloadLimit != 0 {
		t.Fatal("negative limits not normalized")
	}
}

func TestIndexRateLimitClearMissingResource(t *testing.T) {
	s, _ := newRateLimitService()
	for _, mode := range []string{"upload_limit", "download_limit"} {
		if err := s.ClearTunnel(9, mode); !errors.Is(err, ErrTunnelNotFound) {
			t.Fatalf("tunnel: %v", err)
		}
		if err := s.ClearHost(9, mode); !errors.Is(err, ErrHostNotFound) {
			t.Fatalf("host: %v", err)
		}
	}
}
