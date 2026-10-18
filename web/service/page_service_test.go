package service

import (
	"errors"
	"testing"

	"github.com/djylb/nps/lib/servercfg"
)

type stubSystemService struct {
	info   SystemInfo
	bridge BridgeDisplay
}

func (s stubSystemService) Info() SystemInfo {
	return s.info
}

func (s stubSystemService) BridgeDisplay(*servercfg.Snapshot, string) BridgeDisplay {
	return s.bridge
}

func (stubSystemService) RegisterManagementAccess(string) {}
func (stubSystemService) UpdateServer() (string, error)   { return "", nil }

func TestDefaultPageServiceBuildHostList(t *testing.T) {
	service := DefaultPageService{
		System: stubSystemService{
			info: SystemInfo{Version: "test-version", Year: 2099},
			bridge: BridgeDisplay{
				Primary: BridgeEndpoint{Type: "tls", Addr: "bridge.example:443", IP: "bridge.example", Port: "443"},
				Path:    "/ws",
				TCP:     BridgeTransport{Enabled: true, IP: "bridge.example", Port: "4444"},
				WS:      BridgeTransport{Enabled: true, IP: "bridge.example", Port: "8080"},
			},
		},
	}
	result, err := service.Build(PageBuildInput{
		Config: &servercfg.Snapshot{
			Web:     servercfg.WebConfig{BaseURL: "/nps"},
			Network: servercfg.NetworkConfig{HTTPProxyPort: 8080, HTTPSProxyPort: 8443},
		},
		Controller: "index",
		Action:     "hostlist",
		Host:       "example.com",
		IsAdmin:    true,
		Username:   "admin",
		Params: map[string]string{
			"client_id": "7",
		},
	})
	if err != nil {
		t.Fatalf("Build(hostlist) error = %v", err)
	}
	if result.Data["name"] != "host list" {
		t.Fatalf("name = %v, want host list", result.Data["name"])
	}
	if result.Data["client_id"] != "7" {
		t.Fatalf("client_id = %v, want 7", result.Data["client_id"])
	}
	if result.Data["httpProxyPort"] != "8080" {
		t.Fatalf("httpProxyPort = %v, want 8080", result.Data["httpProxyPort"])
	}
	if result.Data["httpsProxyPort"] != "8443" {
		t.Fatalf("httpsProxyPort = %v, want 8443", result.Data["httpsProxyPort"])
	}
	if result.Data["version"] != "test-version" || result.Data["year"] != 2099 {
		t.Fatalf("version/year = %v/%v, want test-version/2099", result.Data["version"], result.Data["year"])
	}
	if result.Data["bridgeType"] != "tls" || result.Data["addr"] != "bridge.example:443" {
		t.Fatalf("bridge = %v/%v, want tls/bridge.example:443", result.Data["bridgeType"], result.Data["addr"])
	}
}

func TestDefaultPageServiceBuildUnknownPage(t *testing.T) {
	service := DefaultPageService{}
	_, err := service.Build(PageBuildInput{Controller: "unknown", Action: "missing"})
	if !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("Build(unknown) error = %v, want ErrPageNotFound", err)
	}
}

func TestManagementPageDataCarriesFeatureSwitches(t *testing.T) {
	for _, on := range []bool{true, false} {
		data := managementPageData(stubSystemService{}, PageBuildInput{Config: &servercfg.Snapshot{
			Feature: servercfg.FeatureConfig{
				AllowFlowLimit:    on,
				AllowLocalProxy:   on,
				SystemInfoDisplay: on,
			},
		}})
		for _, key := range []string{"allow_flow_limit", "allow_local_proxy", "system_info_display"} {
			if got, ok := data[key]; !ok || got != on {
				t.Errorf("feature %s = %v (set %v), want %v", key, got, ok, on)
			}
		}
	}
}
