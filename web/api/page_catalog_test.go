package api

import (
	"testing"

	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
)

func TestVisiblePageEntriesMetadataForAnonymous(t *testing.T) {
	cfg := &servercfg.Snapshot{Feature: servercfg.FeatureConfig{AllowUserRegister: true}}
	entries := VisiblePageEntries(cfg, "/nps", AnonymousActor(), webservice.DefaultAuthorizationService{})

	login := findPageEntry(entries, "/nps/login/index")
	if login == nil {
		t.Fatal("expected anonymous page catalog to include /nps/login/index")
	}
	if login.Section != "auth" || login.Template != "login/index.html" || !login.Navigation {
		t.Fatalf("unexpected login metadata: %+v", *login)
	}
	if len(login.Params) != 0 {
		t.Fatalf("login page should not require params, got %+v", login.Params)
	}

	if entry := findPageEntry(entries, "/nps/index/index"); entry != nil {
		t.Fatalf("anonymous page catalog should not include protected index page: %+v", *entry)
	}
}

func TestVisiblePageEntriesMetadataForUser(t *testing.T) {
	cfg := &servercfg.Snapshot{Feature: servercfg.FeatureConfig{AllowUserRegister: true}}
	entries := VisiblePageEntries(cfg, "/nps", UserActor("operator", []int{101}), webservice.DefaultAuthorizationService{})

	clientList := findPageEntry(entries, "/nps/client/list")
	if clientList == nil {
		t.Fatal("expected user page catalog to include /nps/client/list")
	}
	if clientList.Section != "clients" || clientList.Menu != "client" || clientList.Template != "client/list.html" || !clientList.Navigation {
		t.Fatalf("unexpected client list metadata: %+v", *clientList)
	}

	tunnelEdit := findPageEntry(entries, "/nps/index/edit")
	if tunnelEdit == nil {
		t.Fatal("expected user page catalog to include /nps/index/edit")
	}
	if tunnelEdit.Section != "tunnels" || tunnelEdit.Ownership != PageOwnershipTunnel || tunnelEdit.Navigation {
		t.Fatalf("unexpected tunnel edit metadata: %+v", *tunnelEdit)
	}
	if len(tunnelEdit.Params) != 1 || tunnelEdit.Params[0].Name != "id" || tunnelEdit.Params[0].In != "query" || tunnelEdit.Params[0].Type != "int" || !tunnelEdit.Params[0].Required {
		t.Fatalf("unexpected tunnel edit params: %+v", tunnelEdit.Params)
	}

	if entry := findPageEntry(entries, "/nps/global/index"); entry != nil {
		t.Fatalf("regular user page catalog should not include global page: %+v", *entry)
	}
}

func TestVisiblePageEntriesMetadataForGlobalManager(t *testing.T) {
	cfg := &servercfg.Snapshot{Feature: servercfg.FeatureConfig{AllowUserRegister: true}}
	identity := (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "service",
		Provider:      "test",
		SubjectID:     "service:global-manager",
		Username:      "global-manager",
		Permissions:   []string{webservice.PermissionGlobalManage},
	}).Normalize()

	entries := VisiblePageEntries(cfg, "", ActorFromSessionIdentity(identity), webservice.DefaultAuthorizationService{})
	globalIndex := findPageEntry(entries, "/global/index")
	if globalIndex == nil {
		t.Fatal("expected global manager page catalog to include /global/index")
	}
	if globalIndex.Section != "global" || globalIndex.Menu != "global" || globalIndex.Template != "global/index.html" || !globalIndex.Navigation {
		t.Fatalf("unexpected global page metadata: %+v", *globalIndex)
	}
}

func TestVisiblePageEntriesRespectsFeatureFlags(t *testing.T) {
	cfg := &servercfg.Snapshot{}
	entries := VisiblePageEntries(cfg, "", AnonymousActor(), webservice.DefaultAuthorizationService{})

	if entry := findPageEntry(entries, "/login/register"); entry != nil {
		t.Fatalf("register page should be hidden when allow_user_register=false: %+v", *entry)
	}

	specs := AvailablePageSpecs(cfg, SessionPageSpecs())
	for _, spec := range specs {
		if spec.Controller == "login" && spec.Action == "register" {
			t.Fatalf("register page spec should be filtered when allow_user_register=false: %+v", spec)
		}
	}
}

func TestFindPageSpecRespectsFeatureFlags(t *testing.T) {
	enabledCfg := &servercfg.Snapshot{Feature: servercfg.FeatureConfig{AllowUserRegister: true}}
	registerSpec, ok := FindPageSpec(enabledCfg, "login", "register")
	if !ok {
		t.Fatal("expected to find register page when allow_user_register=true")
	}
	if registerSpec.Template != "login/register.html" || registerSpec.Menu != "register" {
		t.Fatalf("unexpected register page spec: %+v", registerSpec)
	}

	disabledCfg := &servercfg.Snapshot{}
	if _, ok := FindPageSpec(disabledCfg, "login", "register"); ok {
		t.Fatal("register page should not resolve when allow_user_register=false")
	}

	indexSpec, ok := FindPageSpec(disabledCfg, "index", "index")
	if !ok {
		t.Fatal("expected to find protected index page")
	}
	if indexSpec.Template != "index/index.html" || indexSpec.Section != "dashboard" {
		t.Fatalf("unexpected index page spec: %+v", indexSpec)
	}
}

func findPageEntry(entries []PageEntry, directPath string) *PageEntry {
	for i := range entries {
		if entries[i].DirectPath == directPath {
			return &entries[i]
		}
	}
	return nil
}
