package file

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/index"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/obs"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/lib/textutil"
)

var log = obs.Module("store")

type DbUtils struct {
	JsonDb *JsonDb
	events entryEvents
}

// New returns an empty store kept in the conf directory of runPath.
func New(runPath string) *DbUtils {
	return &DbUtils{JsonDb: NewJsonDb(runPath)}
}

// Open returns the store kept in the conf directory of runPath, loaded from
// its files.
func Open(runPath string) (*DbUtils, error) {
	d := New(runPath)
	for _, load := range []func() error{
		d.JsonDb.LoadClientFromJsonFile,
		d.JsonDb.LoadTaskFromJsonFile,
		d.JsonDb.LoadHostFromJsonFile,
		d.JsonDb.LoadGlobalFromJsonFile,
	} {
		if err := load(); err != nil {
			return nil, fmt.Errorf("load database: %w", err)
		}
	}
	return d, nil
}

func GetMapKeys(m *sync.Map, isSort bool, sortKey, order string) (keys []int) {
	if (sortKey == "InletFlow" || sortKey == "ExportFlow") && isSort {
		return sortClientByKey(m, sortKey, order)
	}
	m.Range(func(key, value interface{}) bool {
		keys = append(keys, key.(int))
		return true
	})
	sort.Ints(keys)
	// Sample once per client before sorting, including the multi-client
	// visibility path which pages directly from these keys.
	switch sortKey {
	case "speed", "upload_rate", "download_rate", "Speed", "UploadRate", "DownloadRate":
		if isSort {
			values := make(map[int]int64, len(keys))
			for _, key := range keys {
				if value, ok := m.Load(key); ok {
					if client, ok := value.(*model.Client); ok && client != nil {
						upload, download := client.CurrentRates()
						switch sortKey {
						case "upload_rate", "UploadRate":
							values[key] = upload
						case "download_rate", "DownloadRate":
							values[key] = download
						default:
							values[key] = upload + download
						}
					}
				}
			}
			sort.SliceStable(keys, func(i, j int) bool {
				if order == "asc" {
					return values[keys[i]] < values[keys[j]]
				}
				return values[keys[i]] > values[keys[j]]
			})
		}
	}
	return
}

func (s *DbUtils) GetClientList(start, length int, search, sort, order string, clientId int) ([]*model.Client, int) {
	list := make([]*model.Client, 0)
	var cnt int
	originLength := length
	id := textutil.GetIntNoErrByStr(search)
	keys := GetMapKeys(&s.JsonDb.Clients, true, sort, order)
	for _, key := range keys {
		if value, ok := s.JsonDb.Clients.Load(key); ok {
			v := value.(*model.Client)
			if v.NoDisplay {
				continue
			}
			if clientId != 0 && clientId != v.Id {
				continue
			}
			if search != "" && v.Id != id && !textutil.ContainsFold(v.VerifyKey, search) && !textutil.ContainsFold(v.Remark, search) {
				continue
			}
			cnt++
			if start--; start < 0 {
				if originLength == 0 {
					list = append(list, v)
				} else if length--; length >= 0 {
					list = append(list, v)
				}
			}
		}
	}
	return list, cnt
}

func (s *DbUtils) GetIdByVerifyKey(vKey, addr, localAddr string, hashFunc func(string) string) (id int, err error) {
	var exist bool
	s.JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*model.Client)
		if hashFunc(v.VerifyKey) == vKey && v.Status && v.Id > 0 {
			v.Addr = netaddr.Host(addr)
			v.LocalAddr = netaddr.Host(localAddr)
			id = v.Id
			exist = true
			return false
		}
		return true
	})
	if exist {
		return
	}
	return 0, errors.New("not found")
}

// ReplaceClientVKey moves the authentication index entry of client id from
// the verify key oldKey to newKey.
func (s *DbUtils) ReplaceClientVKey(oldKey, newKey string, id int) {
	if id <= 0 {
		return
	}
	s.JsonDb.vkeyIndex.Remove(crypt.Blake2b(oldKey))
	s.JsonDb.vkeyIndex.Add(crypt.Blake2b(newKey), id)
}

func (s *DbUtils) GetClientIdByBlake2bVkey(vkey string) (id int, err error) {
	var exist bool
	id, exist = s.JsonDb.vkeyIndex.Get(vkey)
	if exist && id > 0 {
		return
	}
	err = errors.New("can not find client")
	return
}

func (s *DbUtils) GetClientIdByMd5Vkey(vkey string) (id int, err error) {
	var exist bool
	s.JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*model.Client)
		if v.Id > 0 && crypt.Md5(v.VerifyKey) == vkey {
			exist = true
			id = v.Id
			return false
		}
		return true
	})
	if exist {
		return
	}
	err = errors.New("can not find client")
	return
}

// newEntryFlow returns the counter of a new task or host: zeroed traffic that
// keeps the flow and time limits requested in f. A fresh Flow is always
// allocated so the counter is never shared with the caller's object.
func newEntryFlow(f *model.Flow) *model.Flow {
	if f == nil {
		return new(model.Flow)
	}
	f.RLock()
	defer f.RUnlock()
	return &model.Flow{FlowLimit: f.FlowLimit, TimeLimit: f.TimeLimit}
}

// NewTask adds t. A password explicitly set on a task created through the
// web or API that is already used by another task is rejected; for tasks
// delivered by a client configuration (NoStore) it is replaced by a random one,
// see taskPasswords.reserve.
func (s *DbUtils) NewTask(t *model.Tunnel) error {
	if err := s.JsonDb.taskPasswords.reserve(t, !t.NoStore); err != nil {
		return err
	}
	t.Flow = newEntryFlow(t.Flow)
	normalizeTask(t)
	s.JsonDb.Tasks.Store(t.Id, t)
	logStoreError(s.JsonDb.StoreTasksToJsonFile())
	s.tunnelCreated(t)
	return nil
}

// UpdateTask stores t in place of the task with the same id. t may be the
// stored object itself or an edited copy; the password the task held before
// is tracked separately and released when it changes. A rejected password
// leaves the stored task and the password index unchanged.
func (s *DbUtils) UpdateTask(t *model.Tunnel) error {
	if err := s.JsonDb.taskPasswords.reserve(t, !t.NoStore); err != nil {
		return err
	}
	if old, ok := s.JsonDb.Tasks.Load(t.Id); ok {
		if previous, ok := old.(*model.Tunnel); ok && previous != nil && previous != t {
			t.AdoptRuntime(previous)
		}
	}
	normalizeTask(t)
	s.JsonDb.Tasks.Store(t.Id, t)
	logStoreError(s.JsonDb.StoreTasksToJsonFile())
	s.tunnelSaved(t)
	return nil
}

// ReserveTaskPassword claims t.Password for t.Id under the same rules as
// NewTask and UpdateTask. It is used before an existing task is updated in
// place from t.
func (s *DbUtils) ReserveTaskPassword(t *model.Tunnel) error {
	return s.JsonDb.taskPasswords.reserve(t, !t.NoStore)
}

// taskPasswords holds the password every task claims, so that two tasks
// can never claim the same password.
type taskPasswords struct {
	mu     sync.Mutex
	index  *index.StringIDIndex // password hash -> task id
	claims map[int]string       // task id -> the password hash it holds in index
}

func newTaskPasswords() *taskPasswords {
	return &taskPasswords{index: index.NewStringIDIndex(), claims: make(map[int]string)}
}

func (p *taskPasswords) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.index.Clear()
	p.claims = make(map[int]string)
}

func (p *taskPasswords) release(id int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.releaseLocked(id)
}

// releaseLocked drops the password claim of task id.
func (p *taskPasswords) releaseLocked(id int) {
	if hash, ok := p.claims[id]; ok {
		if idxId, ok := p.index.Get(hash); ok && idxId == id {
			p.index.Remove(hash)
		}
		delete(p.claims, id)
	}
}

// lookup returns the task id that claims the password of md5 hash.
func (p *taskPasswords) lookup(hash string) (int, bool) { return p.index.Get(hash) }

// reserve makes t.Password the password claimed by t.Id and releases the
// one the task held before. A secret or p2p task without a password gets a
// random one, regenerated until it is unique. An explicitly set password
// that another task already uses is rejected when strict, leaving t and the
// index unchanged; otherwise it is logged and replaced by a random one.
func (p *taskPasswords) reserve(t *model.Tunnel, strict bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	password := t.Password
	generated := false
	if (t.Mode == "secret" || t.Mode == "p2p") && password == "" {
		password = crypt.GetRandomString(16, t.Id)
		generated = true
	}
	if password == "" {
		p.releaseLocked(t.Id)
		return nil
	}
	for {
		idxId, ok := p.index.Get(crypt.Md5(password))
		if !ok || idxId == t.Id {
			break
		}
		if !generated {
			if strict {
				return fmt.Errorf("task password is already used by task %d", idxId)
			}
			log.Warn("tunnel password already used, a random one is used instead", "tunnel", t.Id, "used_by", idxId)
			generated = true
		}
		password = crypt.GetRandomString(16, t.Id)
	}
	t.Password = password
	hash := crypt.Md5(password)
	if p.claims[t.Id] != hash {
		p.releaseLocked(t.Id)
	}
	p.index.Add(hash, t.Id)
	p.claims[t.Id] = hash
	return nil
}

func normalizeTask(t *model.Tunnel) {
	switch t.Mode {
	case "socks5":
		t.Mode = "mixProxy"
		t.HttpProxy = false
		t.Socks5Proxy = true
	case "httpProxy":
		t.Mode = "mixProxy"
		t.HttpProxy = true
		t.Socks5Proxy = false
	}
	t.TargetType = model.NormalizeTargetType(t.TargetType)
	t.CompileDestACL()
	t.CompileSourceLists(acl.ScopeUser, taskOwner(t))
}

// logStoreError reports a failed write of the json store. The in-memory state
// stays authoritative and is written again by the next store.
func logStoreError(err error) {
	if err != nil {
		log.Error("store not written, changes are kept in memory until the next successful write", "err", err)
	}
}

func (s *DbUtils) SaveGlobal(t *model.Glob) error {
	t.CompileSourceLists(acl.ScopeGlobal, "the global settings")
	t.ApplyLimits()
	s.JsonDb.Global = t
	logStoreError(s.JsonDb.StoreGlobalToJsonFile())
	return nil
}

func (s *DbUtils) DelTask(id int) error {
	s.JsonDb.taskPasswords.release(id)
	v, existed := s.JsonDb.Tasks.LoadAndDelete(id)
	logStoreError(s.JsonDb.StoreTasksToJsonFile())
	if existed {
		s.tunnelDeleted(v.(*model.Tunnel))
	}
	return nil
}

// GetTaskByMd5Password md5 password
func (s *DbUtils) GetTaskByMd5Password(p string) (t *model.Tunnel) {
	id, ok := s.JsonDb.taskPasswords.lookup(p)
	if ok {
		if v, ok := s.JsonDb.Tasks.Load(id); ok {
			t = v.(*model.Tunnel)
			return
		}
	}
	return
}

func (s *DbUtils) GetTask(id int) (t *model.Tunnel, err error) {
	if v, ok := s.JsonDb.Tasks.Load(id); ok {
		t = v.(*model.Tunnel)
		return
	}
	err = errors.New("not found")
	return
}

func (s *DbUtils) DelHost(id int) error {
	v, existed := s.JsonDb.Hosts.LoadAndDelete(id)
	if existed {
		s.JsonDb.hostIndex.Remove(v.(*model.Host).Host, id)
	}
	logStoreError(s.JsonDb.StoreHostToJsonFile())
	if existed {
		s.hostDeleted(v.(*model.Host))
	}
	return nil
}

func (s *DbUtils) IsHostExist(h *model.Host) bool {
	var exist bool
	if h.Location == "" {
		h.Location = "/"
	}
	s.JsonDb.Hosts.Range(func(key, value interface{}) bool {
		v := value.(*model.Host)
		if v.Location == "" {
			v.Location = "/"
		}
		if v.Id != h.Id && v.Host == h.Host && h.Location == v.Location && (v.Scheme == "all" || v.Scheme == h.Scheme) {
			exist = true
			return false
		}
		return true
	})
	return exist
}

func (s *DbUtils) IsHostModify(h *model.Host) bool {
	if h == nil {
		return true
	}

	existingHost, err := s.GetHostById(h.Id)
	if err != nil {
		return true
	}

	if existingHost.IsClose != h.IsClose ||
		existingHost.Host != h.Host ||
		existingHost.Location != h.Location ||
		existingHost.Scheme != h.Scheme ||
		existingHost.HttpsJustProxy != h.HttpsJustProxy ||
		existingHost.CertFile != h.CertFile ||
		existingHost.KeyFile != h.KeyFile {
		return true
	}

	return false
}

func (s *DbUtils) NewHost(t *model.Host) error {
	if t.Location == "" {
		t.Location = "/"
	}
	if t.Scheme != "all" && t.Scheme != "http" && t.Scheme != "https" {
		t.Scheme = "all"
	}
	if s.IsHostExist(t) {
		return errors.New("host has exist")
	}
	s.JsonDb.hostIndex.Add(t.Host, t.Id)
	t.CertType = crypt.GetCertType(t.CertFile)
	t.CertHash = crypt.FNV1a64(t.CertType, t.CertFile, t.KeyFile)
	t.Flow = newEntryFlow(t.Flow)
	t.CompileSourceLists(acl.ScopeUser, hostOwner(t))
	s.JsonDb.Hosts.Store(t.Id, t)
	logStoreError(s.JsonDb.StoreHostToJsonFile())
	s.hostCreated(t)
	return nil
}

// SaveHost stores the entry h, edited in place, which was named oldHost before.
func (s *DbUtils) SaveHost(h *model.Host, oldHost string) error {
	if oldHost != "" && oldHost != h.Host {
		s.JsonDb.hostIndex.Remove(oldHost, h.Id)
		s.JsonDb.hostIndex.Add(h.Host, h.Id)
	}
	err := s.JsonDb.StoreHostToJsonFile()
	s.HostChanged(h)
	return err
}

func (s *DbUtils) GetHost(start, length int, id int, search string) ([]*model.Host, int) {
	list := make([]*model.Host, 0)
	var cnt int
	originLength := length
	searchId := textutil.GetIntNoErrByStr(search)
	keys := GetMapKeys(&s.JsonDb.Hosts, false, "", "")
	for _, key := range keys {
		if value, ok := s.JsonDb.Hosts.Load(key); ok {
			v := value.(*model.Host)
			if search != "" && v.Id != searchId && !textutil.ContainsFold(v.Host, search) && !textutil.ContainsFold(v.Remark, search) && !textutil.ContainsFold(v.Client.VerifyKey, search) {
				continue
			}
			if id == 0 || v.Client.Id == id {
				cnt++
				if start--; start < 0 {
					if originLength == 0 {
						list = append(list, v)
					} else if length--; length >= 0 {
						list = append(list, v)
					}
				}
			}
		}
	}
	return list, cnt
}

func (s *DbUtils) DelClient(id int) error {
	if v, ok := s.JsonDb.Clients.Load(id); ok {
		c := v.(*model.Client)
		if c.Id > 0 {
			s.JsonDb.vkeyIndex.Remove(crypt.Blake2b(c.VerifyKey))
		}
		if c.Rate != nil {
			c.Rate.Stop()
		}
	}
	s.JsonDb.Clients.Delete(id)
	logStoreError(s.JsonDb.StoreClientsToJsonFile())
	return nil
}

func (s *DbUtils) NewClient(c *model.Client) error {
	if c.ConnectionHistory == nil {
		c.ConnectionHistory = new(model.ConnectionHistory)
	}
	var isNotSet bool
	if c.WebUserName != "" && !s.VerifyUserName(c.WebUserName, c.Id) {
		return errors.New("web login username duplicate, please reset")
	}
	c.EnsureWebPassword()
reset:
	if c.VerifyKey == "" || isNotSet {
		isNotSet = true
		c.VerifyKey = crypt.GetRandomString(16, c.Id)
	}
	if !s.VerifyVkey(c.VerifyKey, c.Id) {
		if isNotSet {
			goto reset
		}
		return errors.New("vkey duplicate, please reset")
	}
	if c.RateLimit == 0 {
		c.Rate = rate.NewRate(0)
	} else if c.Rate == nil {
		c.Rate = rate.NewRate(int64(c.RateLimit) * 1024)
	}
	c.Rate.Start()
	if c.Id == 0 {
		c.Id = int(s.JsonDb.GetClientId())
	}
	if c.Flow == nil {
		c.Flow = new(model.Flow)
	}
	c.Flow.MarkClient()
	c.CompileSourceLists(acl.ScopeUser, clientOwner(c))
	s.JsonDb.Clients.Store(c.Id, c)
	if c.Id > 0 {
		s.JsonDb.vkeyIndex.Add(crypt.Blake2b(c.VerifyKey), c.Id)
	}
	logStoreError(s.JsonDb.StoreClientsToJsonFile())
	return nil
}

func (s *DbUtils) VerifyVkey(vkey string, id int) (res bool) {
	res = true
	s.JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*model.Client)
		if v.VerifyKey == vkey && v.Id != id {
			res = false
			return false
		}
		return true
	})
	return res
}

func (s *DbUtils) VerifyUserName(username string, id int) (res bool) {
	res = true
	s.JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*model.Client)
		if v.WebUserName == username && v.Id != id {
			res = false
			return false
		}
		return true
	})
	return res
}

func (s *DbUtils) UpdateClient(t *model.Client) error {
	if v, ok := s.JsonDb.Clients.Load(t.Id); ok {
		c := v.(*model.Client)
		t.ConnectionHistory = c.ConnectionHistory
		if c.Id > 0 {
			s.JsonDb.vkeyIndex.Remove(crypt.Blake2b(c.VerifyKey))
		}
		if c.Rate != nil {
			t.Rate = c.Rate
		}
	}

	if t.Flow == nil {
		t.Flow = new(model.Flow)
	}
	t.Flow.MarkClient()
	t.CompileSourceLists(acl.ScopeUser, clientOwner(t))
	s.JsonDb.Clients.Store(t.Id, t)
	if t.Id > 0 {
		s.JsonDb.vkeyIndex.Add(crypt.Blake2b(t.VerifyKey), t.Id)
	}
	var limit int64
	if t.RateLimit > 0 {
		limit = int64(t.RateLimit) * 1024
	} else {
		limit = 0
	}
	if t.Rate != nil {
		if t.Rate.Limit() != limit {
			t.Rate.ResetLimit(limit)
		} else {
			t.Rate.Start()
		}
	} else {
		t.Rate = rate.NewRate(limit)
		t.Rate.Start()
	}
	return nil
}

func (s *DbUtils) IsPubClient(id int) bool {
	client, err := s.GetClient(id)
	return err == nil && (client.KeyKind == model.KeyPublic)
}

func (s *DbUtils) GetClient(id int) (c *model.Client, err error) {
	if v, ok := s.JsonDb.Clients.Load(id); ok {
		c = v.(*model.Client)
		return
	}
	err = errors.New("can not find client")
	return
}

// RangeTunnels calls fn for every stored tunnel until fn returns false.
func (s *DbUtils) RangeTunnels(fn func(*model.Tunnel) bool) {
	s.JsonDb.Tasks.Range(func(_, v any) bool { return fn(v.(*model.Tunnel)) })
}

// RangeHosts calls fn for every stored host until fn returns false.
func (s *DbUtils) RangeHosts(fn func(*model.Host) bool) {
	s.JsonDb.Hosts.Range(func(_, v any) bool { return fn(v.(*model.Host)) })
}

// NextTunnelID reserves the id of a new tunnel.
func (s *DbUtils) NextTunnelID() int { return int(s.JsonDb.GetTaskId()) }

// NextHostID reserves the id of a new host.
func (s *DbUtils) NextHostID() int { return int(s.JsonDb.GetHostId()) }

func (s *DbUtils) GetGlobal() (c *model.Glob) {
	return s.JsonDb.Global
}

func (s *DbUtils) GetHostById(id int) (h *model.Host, err error) {
	if v, ok := s.JsonDb.Hosts.Load(id); ok {
		h = v.(*model.Host)
		return
	}
	err = errors.New("the host could not be parsed")
	return
}

// GetInfoByHost get key by host from x
func (s *DbUtils) GetInfoByHost(host string, r *http.Request) (h *model.Host, err error) {
	host = strings.TrimSuffix(strings.ToLower(netaddr.Host(host)), ".")
	hostLength := len(host)

	requestPath := r.RequestURI
	if requestPath == "" {
		requestPath = "/"
	}

	scheme := r.URL.Scheme

	ids := s.JsonDb.hostIndex.Lookup(host)
	if len(ids) == 0 {
		return nil, errors.New("the host could not be parsed")
	}

	var bestMatch *model.Host
	var bestDomainLength int
	var bestLocationLength int
	for _, id := range ids {
		value, ok := s.JsonDb.Hosts.Load(id)
		if !ok {
			continue
		}
		v := value.(*model.Host)

		if v.IsClose || (v.Scheme != "all" && v.Scheme != scheme) {
			continue
		}

		curDomainLength := len(strings.TrimPrefix(v.Host, "*"))
		if hostLength < curDomainLength {
			continue
		}

		name := strings.ToLower(v.Host)
		equaled := name == host
		matched := equaled || (strings.HasPrefix(name, "*") && strings.HasSuffix(host, name[1:]))
		if !matched {
			continue
		}

		location := v.Location
		if location == "" {
			location = "/"
		}

		if !strings.HasPrefix(requestPath, location) {
			continue
		}

		curLocationLength := len(location)
		if bestMatch == nil {
			bestMatch = v
			bestDomainLength = curDomainLength
			bestLocationLength = curLocationLength
			continue
		}
		if curLocationLength > bestLocationLength {
			bestMatch = v
			bestDomainLength = curDomainLength
			bestLocationLength = curLocationLength
			continue
		}
		if curLocationLength == bestLocationLength {
			if curDomainLength > bestDomainLength {
				bestMatch = v
				bestDomainLength = curDomainLength
				bestLocationLength = curLocationLength
				continue
			}
			if equaled {
				bestMatch = v
				bestDomainLength = curDomainLength
				bestLocationLength = curLocationLength
				continue
			}
		}
	}

	if bestMatch != nil {
		return bestMatch, nil
	}
	return nil, errors.New("the host could not be parsed")
}

// FindCertByHost returns the host entry that terminates TLS for the server
// name host. An entry for the exact name beats a wildcard one, and a longer
// wildcard suffix a shorter one; among entries for the same name, one with
// TLS configured (a certificate or AutoSSL) wins, then the one with the
// shortest location, then the lowest id, so the choice never depends on map
// order. Names compare case-insensitively, ignoring a trailing dot.
func (s *DbUtils) FindCertByHost(host string) (*model.Host, error) {
	return s.findCertHost(host, nil)
}

// FindReusableCert returns the entry whose certificate FindCertByHost would
// choose for the server name host among those with certificate files, leaving
// out the entry excludeID and, unless clientID is 0, entries of other clients.
func (s *DbUtils) FindReusableCert(host string, excludeID, clientID int) (*model.Host, error) {
	return s.findCertHost(host, func(v *model.Host) bool {
		return v.HasCert() && v.Id != excludeID && (clientID == 0 || (v.Client != nil && v.Client.Id == clientID))
	})
}

func (s *DbUtils) findCertHost(host string, accept func(*model.Host) bool) (*model.Host, error) {
	host = strings.TrimSuffix(strings.ToLower(netaddr.Host(host)), ".")
	if host == "" {
		return nil, errors.New("invalid Host")
	}

	var best *model.Host
	var bestRank certHostRank
	for _, id := range s.JsonDb.hostIndex.Lookup(host) {
		value, ok := s.JsonDb.Hosts.Load(id)
		if !ok {
			continue
		}
		v := value.(*model.Host)
		if v.IsClose || v.Scheme == "http" || (accept != nil && !accept(v)) {
			continue
		}
		rank, ok := rankCertHost(v, host)
		if ok && (best == nil || rank.better(bestRank)) {
			best, bestRank = v, rank
		}
	}
	if best != nil {
		return best, nil
	}
	return nil, errors.New("the host could not be parsed")
}

type certHostRank struct {
	suffix   int
	location int
	id       int
	exact    bool
	tls      bool
}

func (r certHostRank) better(o certHostRank) bool {
	switch {
	case r.exact != o.exact:
		return r.exact
	case r.suffix != o.suffix:
		return r.suffix > o.suffix
	case r.tls != o.tls:
		return r.tls
	case r.location != o.location:
		return r.location < o.location
	default:
		return r.id < o.id
	}
}

// rankCertHost reports whether the entry v serves the lower-case name host
// and how specifically: "*.example.com" serves any name below example.com,
// at any depth, but never example.com itself.
func rankCertHost(v *model.Host, host string) (certHostRank, bool) {
	name := strings.TrimSuffix(strings.ToLower(v.Host), ".")
	r := certHostRank{id: v.Id, location: len(v.Location), tls: v.HasCert() || v.AutoSSL}
	if v.Location == "" {
		r.location = 1
	}
	if name == host {
		r.exact = true
		return r, true
	}
	suffix, ok := strings.CutPrefix(name, "*")
	if !ok || !strings.HasSuffix(host, suffix) {
		return r, false
	}
	r.suffix = len(suffix)
	return r, true
}

// RangeClientCertHosts calls fn, until it returns false, for each host entry
// of the client clientID with a certificate configured.
func (s *DbUtils) RangeClientCertHosts(clientID int, fn func(*model.Host) bool) {
	s.JsonDb.Hosts.Range(func(_, value any) bool {
		v := value.(*model.Host)
		if v.Client == nil || v.Client.Id != clientID || !v.HasCert() {
			return true
		}
		return fn(v)
	})
}

// ClientTunnel returns the stored tunnel of c on the port or with the
// password of t.
func (s *DbUtils) ClientTunnel(c *model.Client, t *model.Tunnel) (tt *model.Tunnel, exist bool) {
	s.JsonDb.Tasks.Range(func(key, value interface{}) bool {
		v := value.(*model.Tunnel)
		if v.Client.Id == c.Id && ((v.Port == t.Port && t.Port != 0) || (v.Password == t.Password && t.Password != "")) {
			exist = true
			tt = v
			return false
		}
		return true
	})
	return
}

// ClientEntryCount returns how many tunnels and hosts c has.
func (s *DbUtils) ClientEntryCount(c *model.Client) (num int) {
	s.JsonDb.Tasks.Range(func(key, value interface{}) bool {
		v := value.(*model.Tunnel)
		if v.Client.Id == c.Id {
			num++
		}
		return true
	})

	s.JsonDb.Hosts.Range(func(key, value interface{}) bool {
		v := value.(*model.Host)
		if v.Client.Id == c.Id {
			num++
		}
		return true
	})
	return
}

// ClientHost returns the stored host of c with the host and location of h.
func (s *DbUtils) ClientHost(c *model.Client, h *model.Host) (hh *model.Host, exist bool) {
	s.JsonDb.Hosts.Range(func(key, value interface{}) bool {
		v := value.(*model.Host)
		if v.Client.Id == c.Id && v.Host == h.Host && h.Location == v.Location {
			exist = true
			hh = v
			return false
		}
		return true
	})
	return
}
