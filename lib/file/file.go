package file

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/index"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/lib/runenv"
)

func NewJsonDb(runPath string) *JsonDb {
	return &JsonDb{
		RunPath:        runPath,
		TaskFilePath:   filepath.Join(runPath, "conf", "tasks.json"),
		HostFilePath:   filepath.Join(runPath, "conf", "hosts.json"),
		ClientFilePath: filepath.Join(runPath, "conf", "clients.json"),
		GlobalFilePath: filepath.Join(runPath, "conf", "global.json"),
		hostIndex:      index.NewDomainIndex(),
		vkeyIndex:      index.NewStringIDIndex(),
		taskPasswords:  newTaskPasswords(),
	}
}

type JsonDb struct {
	Tasks            sync.Map
	Hosts            sync.Map
	HostsTmp         sync.Map
	Clients          sync.Map
	Global           *model.Glob
	RunPath          string
	ClientIncreaseId int32  //client increased id
	TaskIncreaseId   int32  //task increased id
	HostIncreaseId   int32  //host increased id
	TaskFilePath     string //task file path
	HostFilePath     string //host file path
	ClientFilePath   string //client file path
	GlobalFilePath   string //global file path

	entriesChanged atomic.Pointer[func()] // see DbUtils.OnEntriesChanged

	hostIndex     *index.DomainIndex   // host name -> host ids
	vkeyIndex     *index.StringIDIndex // blake2b of a verify key -> client id
	taskPasswords *taskPasswords

	// one lock per file, so that two writes of a file never interleave
	hostLock, taskLock, clientLock, globalLock sync.Mutex
}

func (s *JsonDb) LoadTaskFromJsonFile() error {
	s.taskPasswords.reset()
	converted := false
	defer func() {
		if converted {
			// write the converted tunnels back, so the conversion is done once
			logStoreError(s.StoreTasksToJsonFile())
		}
	}()
	return loadSyncMapFromFile(s.TaskFilePath, model.Tunnel{}, func(v any) {
		var err error
		post := v.(*model.Tunnel)
		if post.Id <= 0 || post.Client == nil {
			log.Warn("tunnel skipped: no id or client", "tunnel", post.Id)
			return
		}
		if post.Client, err = s.GetClient(post.Client.Id); err != nil {
			return
		}
		if post.Flow == nil {
			post.Flow = new(model.Flow)
		}
		post.Flow.SetOwner(tunnelOwnerArgs(post)...)
		if post.Target == nil {
			post.Target = new(model.Target)
		}
		_ = s.taskPasswords.reserve(post, false)
		post.NowConn = 0
		converted = legacyTask(post) || converted
		post.TargetType = model.NormalizeTargetType(post.TargetType)
		post.CompileDestACL()
		post.CompileSourceLists(acl.ScopeUser, taskOwner(post))
		s.Tasks.Store(post.Id, post)
		if post.Id > int(s.TaskIncreaseId) {
			s.TaskIncreaseId = int32(post.Id)
		}
	})
}

func (s *JsonDb) LoadClientFromJsonFile() error {
	s.vkeyIndex.Clear()
	return loadSyncMapFromFile(s.ClientFilePath, model.Client{}, func(v any) {
		post := v.(*model.Client)
		if post.ConnectionHistory == nil {
			post.ConnectionHistory = new(model.ConnectionHistory)
		}
		if post.Id == 0 {
			log.Warn("client skipped: no id")
			return
		}
		if post.Cnf == nil {
			post.Cnf = new(model.Config)
		}
		if post.Id > 0 {
			s.vkeyIndex.Add(crypt.Blake2b(post.VerifyKey), post.Id)
		}
		if post.RateLimit > 0 {
			post.Rate = rate.NewRate(int64(post.RateLimit) * 1024)
		} else {
			post.Rate = rate.NewRate(0)
		}
		post.Rate.Start()
		post.NowConn = 0
		if post.Flow == nil {
			post.Flow = new(model.Flow)
		}
		post.Flow.MarkClient()
		post.Flow.SetOwner("client", post.Id)
		post.CompileSourceLists(acl.ScopeUser, clientOwner(post))
		s.Clients.Store(post.Id, post)
		if post.Id > int(s.ClientIncreaseId) {
			s.ClientIncreaseId = int32(post.Id)
		}
	})
}

func (s *JsonDb) LoadHostFromJsonFile() error {
	s.hostIndex.Destroy()
	return loadSyncMapFromFile(s.HostFilePath, model.Host{}, func(v any) {
		var err error
		post := v.(*model.Host)
		if post.Id <= 0 || post.Client == nil {
			log.Warn("host skipped: no id or client", "host", post.Id)
			return
		}
		if post.Client, err = s.GetClient(post.Client.Id); err != nil {
			return
		}
		if post.Flow == nil {
			post.Flow = new(model.Flow)
		}
		if post.Target == nil {
			post.Target = new(model.Target)
		}
		if post.CertType == "" {
			post.CertType = crypt.GetCertType(post.CertFile)
		}
		if post.CertHash == "" {
			post.CertHash = crypt.FNV1a64(post.CertType, post.CertFile, post.KeyFile)
		}
		post.NowConn = 0
		post.Flow.SetOwner(hostOwnerArgs(post)...)
		post.CompileSourceLists(acl.ScopeUser, hostOwner(post))
		s.Hosts.Store(post.Id, post)
		s.hostIndex.Add(post.Host, post.Id)
		if post.Id > int(s.HostIncreaseId) {
			s.HostIncreaseId = int32(post.Id)
		}
	})
}

func (s *JsonDb) LoadGlobalFromJsonFile() error {
	err := loadSyncMapFromFileWithSingleJson(s.GlobalFilePath, func(v string) {
		post := new(model.Glob)
		// "null", as older versions stored empty settings, leaves post nil
		if strings.TrimSpace(v) == "" {
			return
		}
		if err := json.Unmarshal([]byte(v), &post); err != nil {
			log.Error("store file not readable", "file", s.GlobalFilePath, "err", err)
			backupUnreadable(s.GlobalFilePath, []byte(v))
			return
		}
		if post == nil {
			return
		}
		post.CompileSourceLists(acl.ScopeGlobal, "the global settings")
		post.ApplyLimits()
		rate.SetGlobalTraffic(post.InletFlow, post.ExportFlow)
		s.Global = post
	})
	if s.Global == nil {
		g := new(model.Glob)
		g.CompileSourceLists(acl.ScopeGlobal, "the global settings")
		s.Global = g
	}
	return err
}

func clientOwner(c *model.Client) string { return "client " + strconv.Itoa(c.Id) }
func taskOwner(t *model.Tunnel) string   { return "tunnel " + strconv.Itoa(t.Id) }
func hostOwner(h *model.Host) string     { return "host " + strconv.Itoa(h.Id) + " (" + h.Host + ")" }

func (s *JsonDb) GetClient(id int) (c *model.Client, err error) {
	if v, ok := s.Clients.Load(id); ok {
		c = v.(*model.Client)
		return
	}
	err = errors.New("can not find client")
	return
}

// OnEntriesChanged makes f called after tunnels or hosts of s were stored
// or changed in place, without any lock held; f must return promptly. The
// server that owns s sets it before it starts.
func (s *DbUtils) OnEntriesChanged(f func()) { s.JsonDb.entriesChanged.Store(&f) }

func (s *JsonDb) notifyEntriesChanged() {
	if f := s.entriesChanged.Load(); f != nil {
		(*f)()
	}
}

func (s *JsonDb) StoreHostToJsonFile() error {
	defer s.notifyEntriesChanged()
	s.hostLock.Lock()
	defer s.hostLock.Unlock()
	return storeSyncMapToFile(&s.Hosts, s.HostFilePath)
}

func (s *JsonDb) StoreTasksToJsonFile() error {
	defer s.notifyEntriesChanged()
	s.taskLock.Lock()
	defer s.taskLock.Unlock()
	return storeSyncMapToFile(&s.Tasks, s.TaskFilePath)
}

func (s *JsonDb) StoreClientsToJsonFile() error {
	s.clientLock.Lock()
	defer s.clientLock.Unlock()
	return storeSyncMapToFile(&s.Clients, s.ClientFilePath)
}

func (s *JsonDb) StoreGlobalToJsonFile() error {
	s.globalLock.Lock()
	defer s.globalLock.Unlock()
	return storeGlobalToFile(s.Global, s.GlobalFilePath)
}

func (s *JsonDb) GetClientId() int32 {
	return atomic.AddInt32(&s.ClientIncreaseId, 1)
}

func (s *JsonDb) GetTaskId() int32 {
	return atomic.AddInt32(&s.TaskIncreaseId, 1)
}

func (s *JsonDb) GetHostId() int32 {
	return atomic.AddInt32(&s.HostIncreaseId, 1)
}

func loadSyncMapFromFile(filePath string, t any, f func(value any)) error {
	// 如果文件不存在，则创建空文件
	if !runenv.FileExists(filePath) {
		if err := createEmptyFile(filePath); err != nil {
			return err
		}
	}

	// 读取文件内容
	b, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	// 加载新的json文件，是一个正常的json数组文件
	err = loadJsonFile(b, t, f)

	if err != nil {
		log.Warn("store file not readable, loading it as the obsolete format", "file", filePath, "err", err)
		backupUnreadable(filePath, b)
		// 加载新json报错，则加载旧json
		loadObsoleteJsonFile(b, t, f)
	}
	return nil
}

// backupUnreadable keeps the content of a file that failed to parse as
// filePath.bad, as the next save overwrites the file with what could be read.
func backupUnreadable(filePath string, content []byte) {
	if len(bytes.TrimSpace(content)) == 0 {
		return
	}
	if err := os.WriteFile(filePath+".bad", content, 0o600); err != nil {
		log.Error("unreadable store file not backed up", "file", filePath, "err", err)
		return
	}
	log.Warn("unreadable store file kept", "file", filePath+".bad")
}

func loadObsoleteJsonFile(b []byte, t any, f func(value any)) {
	// 加载旧版的json文件，以"\n"+connDataSeq分隔
	var err error
	// 根据分隔符处理内容
	for v := range strings.SplitSeq(string(b), "\n"+connDataSeq) {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		switch t.(type) {
		case model.Client:
			var client model.Client
			if err = json.Unmarshal([]byte(v), &client); err != nil {
				fmt.Println("Error:", err)
				return
			}
			f(&client)
			//break
		case model.Host:
			var host model.Host
			if err = json.Unmarshal([]byte(v), &host); err != nil {
				fmt.Println("Error:", err)
				return
			}
			f(&host)
			//break
		case model.Tunnel:
			var tunnel model.Tunnel
			if err = json.Unmarshal([]byte(v), &tunnel); err != nil {
				fmt.Println("Error:", err)
				return
			}
			f(&tunnel)
			//break
		}
	}
}

func loadJsonFile(b []byte, t any, f func(value any)) error {
	// 加载新的json文件，是一个正常的json数组文件
	var err error
	switch t.(type) {
	case model.Client:
		var clients []model.Client
		if len(b) != 0 {
			err = json.Unmarshal(b, &clients)
			if err != nil {
				return err
			}
		}
		for i := range clients {
			f(&clients[i])
		}
		//break
	case model.Host:
		var hosts []model.Host
		if len(b) != 0 {
			err = json.Unmarshal(b, &hosts)
			if err != nil {
				return err
			}
		}
		for i := range hosts {
			f(&hosts[i])
		}
		//break
	case model.Tunnel:
		var tunnels []model.Tunnel
		if len(b) != 0 {
			err = json.Unmarshal(b, &tunnels)
			if err != nil {
				return err
			}
		}
		for i := range tunnels {
			f(&tunnels[i])
		}
		//break
	}
	return nil
}

func loadSyncMapFromFileWithSingleJson(filePath string, f func(value string)) error {
	if !runenv.FileExists(filePath) {
		return createEmptyFile(filePath)
	}

	b, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	f(string(b))
	return nil
}

// 创建空文件的辅助函数
func createEmptyFile(filePath string) error {
	dir := filepath.Dir(filePath)
	if !runenv.FileExists(dir) {
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			return err
		}
	}

	if !runenv.FileExists(filePath) {
		file, err := os.Create(filePath)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
	}

	return nil
}

func storeSyncMapToFile(m *sync.Map, filePath string) error {
	return writeFileAtomic(filePath, func(w *bufio.Writer) error {
		if _, err := w.WriteString("[\n"); err != nil {
			return err
		}
		first := true
		var rangeErr error
		m.Range(func(key, value any) bool {
			switch v := value.(type) {
			case *model.Tunnel:
				if v.NoStore {
					return true
				}
			case *model.Host:
				if v.NoStore {
					return true
				}
			case *model.Client:
				if v.NoStore {
					return true
				}
			}
			data, err := json.Marshal(value)
			if err != nil {
				rangeErr = err
				return false
			}
			if !first {
				if _, err = w.WriteString(",\n"); err != nil {
					rangeErr = err
					return false
				}
			}
			first = false
			if _, err = w.WriteString("  "); err != nil {
				rangeErr = err
				return false
			}
			if _, err = w.Write(data); err != nil {
				rangeErr = err
				return false
			}
			return true
		})
		if rangeErr != nil {
			return rangeErr
		}
		_, err := w.WriteString("\n]\n")
		return err
	})
}

func storeGlobalToFile(m *model.Glob, filePath string) error {
	if m != nil {
		in, out := rate.GlobalTraffic()
		m.Lock()
		m.InletFlow, m.ExportFlow = in, out
		m.Unlock()
	}
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("store %s: %w", filePath, err)
	}
	return writeFileAtomic(filePath, func(w *bufio.Writer) error {
		_, err := w.Write(b)
		return err
	})
}

// writeFileAtomic writes the content produced by write into a temporary file
// next to filePath and renames it over filePath once it is fully synced. On any
// failure the temporary file is removed and filePath is left untouched.
func writeFileAtomic(filePath string, write func(w *bufio.Writer) error) (err error) {
	tmpFilePath := filePath + ".tmp"
	file, err := os.Create(tmpFilePath)
	if err != nil {
		return fmt.Errorf("store %s: %w", filePath, err)
	}
	defer func() {
		if err != nil {
			_ = file.Close()
			_ = os.Remove(tmpFilePath)
			err = fmt.Errorf("store %s: %w", filePath, err)
		}
	}()
	writer := bufio.NewWriter(file)
	if err = write(writer); err != nil {
		return err
	}
	if err = writer.Flush(); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmpFilePath, filePath); err != nil {
		return err
	}
	syncDir(filepath.Dir(filePath))
	return nil
}

// syncDir makes a rename in dir durable where the system supports it, which
// Windows does not; the data itself is already on disk.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer func() { _ = d.Close() }()
	_ = d.Sync()
}

// connDataSeq separates the records of the old JSON files.
const connDataSeq = "*#*"
