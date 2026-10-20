package config

import "testing"

func TestEntryDirectionalRateConfig(t *testing.T) {
	task := dealTunnel("upload_limit=123\ndownload_limit=456\nmode=tcp")
	host := dealHost("host=example.com\nupload_limit=12\ndownload_limit=-1")
	if task.UploadLimit != 123 || task.DownloadLimit != 456 || host.UploadLimit != 12 || host.DownloadLimit != 0 {
		t.Fatal("directional rates not parsed")
	}
}
