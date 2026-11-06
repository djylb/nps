package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/file"
)

// configBundleVersion is the version of ConfigBundle's format.
const configBundleVersion = 1

// ConfigBundle is an export of the stored clients, tunnels and domain entries.
// The entries have the form of the json store and hold the secrets it holds;
// their Client fields are read for the id only.
type ConfigBundle struct {
	Version    int            `json:"version"`
	ExportedAt time.Time      `json:"exported_at"`
	Clients    []*file.Client `json:"clients"`
	Tunnels    []*file.Tunnel `json:"tunnels"`
	Hosts      []*file.Host   `json:"hosts"`
	// SkippedFileTunnels counts file mode tunnels, which the web cannot
	// create and which an import would refuse.
	SkippedFileTunnels int `json:"skipped_file_tunnels,omitempty"`
}

// ImportResult counts what an import added.
type ImportResult struct {
	Clients, Tunnels, Hosts int
}

// ImportConfigInput is an import request.
type ImportConfigInput struct {
	Data []byte
	// ReservedAdminUsername cannot be a client's web login.
	ReservedAdminUsername string
}

// ConfigTransferService exports and imports the stored configuration.
type ConfigTransferService interface {
	Export() ([]byte, error)
	Import(ImportConfigInput) (ImportResult, error)
}

// DefaultConfigTransferService goes through the same repository and runtime
// calls as creating the entries from the web.
type DefaultConfigTransferService struct {
	Backend Backend
}

// importLock keeps two imports from passing validation against each other.
var importLock sync.Mutex

func (s DefaultConfigTransferService) Export() ([]byte, error) {
	repo := s.Backend.repo()
	bundle := ConfigBundle{
		Version: configBundleVersion, ExportedAt: time.Now(),
		Clients: []*file.Client{}, Tunnels: []*file.Tunnel{}, Hosts: []*file.Host{},
	}
	exported := make(map[int]bool)
	repo.RangeClients(func(c *file.Client) bool {
		if c.Id > 0 && !c.NoStore {
			exported[c.Id] = true
			bundle.Clients = append(bundle.Clients, c)
		}
		return true
	})
	ownerExported := func(c *file.Client) bool { return c != nil && (c.Id <= 0 || exported[c.Id]) }
	repo.RangeTunnels(func(t *file.Tunnel) bool {
		switch {
		case t.NoStore || !ownerExported(t.Client):
		case t.Mode == "file":
			bundle.SkippedFileTunnels++
		default:
			bundle.Tunnels = append(bundle.Tunnels, t)
		}
		return true
	})
	repo.RangeHosts(func(h *file.Host) bool {
		if !h.NoStore && ownerExported(h.Client) {
			bundle.Hosts = append(bundle.Hosts, h)
		}
		return true
	})
	return json.MarshalIndent(&bundle, "", "  ")
}

// Import adds the entries of a bundle under new ids; nothing existing is
// replaced. A bundle that conflicts with what exists, or with itself, is
// refused as a whole. Conflicts found only while adding, such as a port that
// cannot be bound, undo what was added so far.
func (s DefaultConfigTransferService) Import(input ImportConfigInput) (ImportResult, error) {
	var bundle ConfigBundle
	if err := json.Unmarshal(input.Data, &bundle); err != nil {
		return ImportResult{}, fmt.Errorf("the file is not a configuration export: %w", err)
	}
	if bundle.Version != configBundleVersion {
		return ImportResult{}, fmt.Errorf("unsupported export version %d", bundle.Version)
	}
	importLock.Lock()
	defer importLock.Unlock()
	if err := s.validateBundle(&bundle, input.ReservedAdminUsername); err != nil {
		return ImportResult{}, err
	}
	return s.applyBundle(&bundle)
}

func (s DefaultConfigTransferService) validateBundle(b *ConfigBundle, reserved string) error {
	repo := s.Backend.repo()
	ids := make(map[int]*file.Client, len(b.Clients))
	seenKeys := make(map[string]bool)
	seenLogins := make(map[string]bool)
	for _, c := range b.Clients {
		if c == nil || c.Id <= 0 || ids[c.Id] != nil {
			return errors.New("the export holds a client without a valid, unique id")
		}
		ids[c.Id] = c
		name := "client " + strconv.Itoa(c.Id)
		if c.VerifyKey == "" {
			return errors.New(name + ": empty vkey")
		}
		if seenKeys[c.VerifyKey] || !repo.VerifyVKey(c.VerifyKey, 0) {
			return errors.New(name + ": vkey is already used")
		}
		seenKeys[c.VerifyKey] = true
		if c.RegisterKey != "" {
			if seenKeys[c.RegisterKey] {
				return errors.New(name + ": registration key is already used")
			}
			if err := (DefaultClientService{Backend: s.Backend}).checkRegisterKey(0, c.RegisterKey, c.VerifyKey); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			seenKeys[c.RegisterKey] = true
		}
		if login := c.WebUserName; login != "" {
			if login == reserved || seenLogins[login] || !repo.VerifyUserName(login, 0) {
				return errors.New(name + ": web username is already used")
			}
			seenLogins[login] = true
		}
		if err := validateSourceLists(&c.SourceLists); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	entries := make(map[int]int)
	countEntry := func(owner *file.Client) (int, error) {
		if owner == nil {
			return 0, errors.New("no client")
		}
		if owner.Id <= 0 {
			if _, err := repo.GetClient(owner.Id); err != nil {
				return 0, fmt.Errorf("unknown client %d", owner.Id)
			}
			return owner.Id, nil
		}
		if ids[owner.Id] == nil {
			return 0, fmt.Errorf("client %d is not in the export", owner.Id)
		}
		entries[owner.Id]++
		if c := ids[owner.Id]; c.MaxTunnelNum != 0 && entries[owner.Id] > c.MaxTunnelNum {
			return 0, fmt.Errorf("client %d exceeds its tunnel limit", owner.Id)
		}
		return owner.Id, nil
	}
	ports := make(map[string]bool)
	for _, t := range b.Tunnels {
		if t == nil {
			return errors.New("the export holds an empty tunnel")
		}
		name := "tunnel " + strconv.Itoa(t.Id)
		if _, err := countEntry(t.Client); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		network, hasPort, ok := importableMode(t.Mode)
		if !ok {
			return fmt.Errorf("%s: mode %q cannot be imported", name, t.Mode)
		}
		if hasPort {
			if t.Port <= 0 || t.Port > 65535 {
				return fmt.Errorf("%s: invalid port %d", name, t.Port)
			}
			if t.Status {
				key := network + "/" + strconv.Itoa(t.Port)
				if ports[key] {
					return fmt.Errorf("%s: port %d is used twice", name, t.Port)
				}
				ports[key] = true
				if !s.Backend.runtime().TunnelPortAvailable(t) {
					return fmt.Errorf("%s: %w", name, ErrPortUnavailable)
				}
			}
		}
		if err := validateSourceLists(&t.SourceLists); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		rules, err := normalizeDestRules(t.DestAclRules)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		t.DestAclRules = rules
	}
	for _, h := range b.Hosts {
		if h == nil || strings.TrimSpace(h.Host) == "" {
			return errors.New("the export holds a domain entry without a name")
		}
		name := "host " + h.Host
		if _, err := countEntry(h.Client); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		h.Scheme = normalizeScheme(h.Scheme)
		probe := &file.Host{Host: h.Host, Location: h.Location, Scheme: h.Scheme}
		if repo.HostExists(probe) {
			return fmt.Errorf("%s: %w", name, ErrHostExists)
		}
		if err := validateSourceLists(&h.SourceLists); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// importableMode returns the listener network of a tunnel mode, whether it
// binds a port, and whether an import may create it. File mode is left out:
// the web does not offer it.
func importableMode(mode string) (network string, hasPort, ok bool) {
	switch mode {
	case "tcp", "tcpTrans", "mixProxy", "socks5", "httpProxy":
		return "tcp", true, true
	case "udp":
		return "udp", true, true
	case "secret", "p2p":
		return "", false, true
	}
	return "", false, false
}

// validateSourceLists refuses rules the web would refuse, where loading the
// store only logs and skips them.
func validateSourceLists(l *file.SourceLists) error {
	_, err := AccessListInput{
		Black: strings.Join(l.BlackIpList, "\n"),
		White: strings.Join(l.WhiteIpList, "\n"),
	}.validate(acl.ScopeUser)
	return err
}

func (s DefaultConfigTransferService) applyBundle(b *ConfigBundle) (result ImportResult, err error) {
	repo, runtime := s.Backend.repo(), s.Backend.runtime()
	var undo []func()
	defer func() {
		if err != nil {
			for i := len(undo) - 1; i >= 0; i-- {
				undo[i]()
			}
			result = ImportResult{}
		}
	}()

	owners := make(map[int]*file.Client, len(b.Clients))
	for _, c := range b.Clients {
		oldID := c.Id
		c.Id = repo.NextClientID()
		c.Addr, c.LocalAddr, c.Version, c.LastOnlineTime = "", "", "", ""
		c.IsConnect, c.NoStore, c.NoDisplay = false, false, false
		c.NowConn = 0
		c.Rate, c.ConnectionHistory = nil, nil
		c.ExportFlow.Store(0)
		c.InletFlow.Store(0)
		c.Flow = freshFlow(c.Flow)
		if c.Cnf == nil {
			c.Cnf = new(file.Config)
		}
		if err = repo.CreateClient(c); err != nil {
			return result, fmt.Errorf("client %d: %w", oldID, err)
		}
		undo = append(undo, func() { _ = repo.DeleteClient(c.Id) })
		owners[oldID] = c
		result.Clients++
	}
	owner := func(c *file.Client) (*file.Client, error) {
		if c.Id <= 0 {
			return repo.GetClient(c.Id)
		}
		return owners[c.Id], nil
	}

	for _, t := range b.Tunnels {
		oldID := t.Id
		if t.Client, err = owner(t.Client); err != nil {
			return result, fmt.Errorf("tunnel %d: %w", oldID, err)
		}
		t.Id = repo.NextTunnelID()
		t.NoStore, t.RunStatus = false, false
		t.NowConn = 0
		t.Routes = nil
		if t.Target == nil {
			t.Target = new(file.Target)
		}
		if err = repo.CreateTunnel(t); err != nil {
			return result, fmt.Errorf("tunnel %d: %w", oldID, err)
		}
		undo = append(undo, func() { _ = runtime.DeleteTunnel(t.Id) })
		if t.Status {
			if err = runtime.AddTunnel(t); err != nil {
				return result, fmt.Errorf("tunnel %d: %w", oldID, normalizeRuntimeError(err))
			}
		}
		result.Tunnels++
	}

	for _, h := range b.Hosts {
		oldName := h.Host
		if h.Client, err = owner(h.Client); err != nil {
			return result, fmt.Errorf("host %s: %w", oldName, err)
		}
		h.Id = repo.NextHostID()
		h.NoStore = false
		h.NowConn = 0
		h.Routes = nil
		if h.Target == nil {
			h.Target = new(file.Target)
		}
		if err = repo.CreateHost(h); err != nil {
			if isHostExistsError(err) {
				err = ErrHostExists
			}
			return result, fmt.Errorf("host %s: %w", oldName, err)
		}
		undo = append(undo, func() {
			_ = repo.DeleteHostRecord(h.Id)
			runtime.RemoveHostCache(h.Id)
		})
		result.Hosts++
	}
	return result, nil
}

// freshFlow keeps the limits of f; imported entries start with no traffic.
func freshFlow(f *file.Flow) *file.Flow {
	if f == nil {
		return new(file.Flow)
	}
	return &file.Flow{FlowLimit: f.FlowLimit, TimeLimit: f.TimeLimit}
}
