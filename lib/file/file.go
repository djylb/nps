package file

import (
	"bufio"
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
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/rate"
)

func NewJsonDb(runPath string) *JsonDb {
	return &JsonDb{
		RunPath:        runPath,
		TaskFilePath:   filepath.Join(runPath, "conf", "tasks.json"),
		HostFilePath:   filepath.Join(runPath, "conf", "hosts.json"),
		ClientFilePath: filepath.Join(runPath, "conf", "clients.json"),
		GlobalFilePath: filepath.Join(runPath, "conf", "global.json"),
	}
}

type JsonDb struct {
	Tasks            sync.Map
	Hosts            sync.Map
	HostsTmp         sync.Map
	Clients          sync.Map
	Global           *Glob
	RunPath          string
	ClientIncreaseId int32  //client increased id
	TaskIncreaseId   int32  //task increased id
	HostIncreaseId   int32  //host increased id
	TaskFilePath     string //task file path
	HostFilePath     string //host file path
	ClientFilePath   string //client file path
	GlobalFilePath   string //global file path
}

func (s *JsonDb) LoadTaskFromJsonFile() {
	resetTaskPasswords()
	loadSyncMapFromFile(s.TaskFilePath, Tunnel{}, func(v interface{}) {
		var err error
		post := v.(*Tunnel)
		if post.Id <= 0 || post.Client == nil {
			logs.Warn("Tunnel %d skipped: no id or client", post.Id)
			return
		}
		if post.Client, err = s.GetClient(post.Client.Id); err != nil {
			return
		}
		if post.Flow == nil {
			post.Flow = new(Flow)
		}
		if post.Target == nil {
			post.Target = new(Target)
		}
		_ = reserveTaskPassword(post, false)
		post.NowConn = 0
		switch post.Mode {
		case "socks5":
			post.Mode = "mixProxy"
			post.HttpProxy = false
			post.Socks5Proxy = true
		case "httpProxy":
			post.Mode = "mixProxy"
			post.HttpProxy = true
			post.Socks5Proxy = false
		}
		if post.TargetType != common.ConnTCP && post.TargetType != common.ConnUDP {
			post.TargetType = common.ConnAll
		}
		post.CompileDestACL()
		post.CompileSourceLists(acl.ScopeUser, taskOwner(post))
		s.Tasks.Store(post.Id, post)
		if post.Id > int(s.TaskIncreaseId) {
			s.TaskIncreaseId = int32(post.Id)
		}
	})
}

func (s *JsonDb) LoadClientFromJsonFile() {
	Blake2bVkeyIndex.Clear()
	loadSyncMapFromFile(s.ClientFilePath, Client{}, func(v interface{}) {
		post := v.(*Client)
		if post.ConnectionHistory == nil {
			post.ConnectionHistory = new(ConnectionHistory)
		}
		if post.Id == 0 {
			logs.Warn("Client skipped: no id")
			return
		}
		if post.Cnf == nil {
			post.Cnf = new(Config)
		}
		if post.Id > 0 {
			Blake2bVkeyIndex.Add(crypt.Blake2b(post.VerifyKey), post.Id)
		}
		if post.RateLimit > 0 {
			post.Rate = rate.NewRate(int64(post.RateLimit) * 1024)
		} else {
			post.Rate = rate.NewRate(0)
		}
		post.Rate.Start()
		post.NowConn = 0
		if post.Flow == nil {
			post.Flow = new(Flow)
		}
		post.Flow.MarkClient()
		post.CompileSourceLists(acl.ScopeUser, clientOwner(post))
		s.Clients.Store(post.Id, post)
		if post.Id > int(s.ClientIncreaseId) {
			s.ClientIncreaseId = int32(post.Id)
		}
	})
}

func (s *JsonDb) LoadHostFromJsonFile() {
	HostIndex.Destroy()
	loadSyncMapFromFile(s.HostFilePath, Host{}, func(v interface{}) {
		var err error
		post := v.(*Host)
		if post.Id <= 0 || post.Client == nil {
			logs.Warn("Host %d skipped: no id or client", post.Id)
			return
		}
		if post.Client, err = s.GetClient(post.Client.Id); err != nil {
			return
		}
		if post.Flow == nil {
			post.Flow = new(Flow)
		}
		if post.Target == nil {
			post.Target = new(Target)
		}
		if post.CertType == "" {
			post.CertType = common.GetCertType(post.CertFile)
		}
		if post.CertHash == "" {
			post.CertHash = crypt.FNV1a64(post.CertType, post.CertFile, post.KeyFile)
		}
		post.NowConn = 0
		post.CompileSourceLists(acl.ScopeUser, hostOwner(post))
		s.Hosts.Store(post.Id, post)
		HostIndex.Add(post.Host, post.Id)
		if post.Id > int(s.HostIncreaseId) {
			s.HostIncreaseId = int32(post.Id)
		}
	})
}

func (s *JsonDb) LoadGlobalFromJsonFile() {
	loadSyncMapFromFileWithSingleJson(s.GlobalFilePath, func(v string) {
		post := new(Glob)
		// "null", as older versions stored empty settings, leaves post nil
		if json.Unmarshal([]byte(v), &post) != nil || post == nil {
			return
		}
		post.CompileSourceLists(acl.ScopeGlobal, "the global settings")
		post.ApplyLimits()
		rate.SetGlobalTraffic(post.InletFlow, post.ExportFlow)
		s.Global = post
	})
	if s.Global == nil {
		g := new(Glob)
		g.CompileSourceLists(acl.ScopeGlobal, "the global settings")
		s.Global = g
	}
}

func clientOwner(c *Client) string { return "client " + strconv.Itoa(c.Id) }
func taskOwner(t *Tunnel) string   { return "tunnel " + strconv.Itoa(t.Id) }
func hostOwner(h *Host) string     { return "host " + strconv.Itoa(h.Id) + " (" + h.Host + ")" }

func (s *JsonDb) GetClient(id int) (c *Client, err error) {
	if v, ok := s.Clients.Load(id); ok {
		c = v.(*Client)
		return
	}
	err = errors.New("can not find client")
	return
}

var hostLock sync.Mutex

func (s *JsonDb) StoreHostToJsonFile() error {
	hostLock.Lock()
	defer hostLock.Unlock()
	return storeSyncMapToFile(&s.Hosts, s.HostFilePath)
}

var taskLock sync.Mutex

func (s *JsonDb) StoreTasksToJsonFile() error {
	taskLock.Lock()
	defer taskLock.Unlock()
	return storeSyncMapToFile(&s.Tasks, s.TaskFilePath)
}

var clientLock sync.Mutex

func (s *JsonDb) StoreClientsToJsonFile() error {
	clientLock.Lock()
	defer clientLock.Unlock()
	return storeSyncMapToFile(&s.Clients, s.ClientFilePath)
}

var globalLock sync.Mutex

func (s *JsonDb) StoreGlobalToJsonFile() error {
	globalLock.Lock()
	defer globalLock.Unlock()
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

func loadSyncMapFromFile(filePath string, t interface{}, f func(value interface{})) {
	// 如果文件不存在，则创建空文件
	if !common.FileExists(filePath) {
		if err := createEmptyFile(filePath); err != nil {
			panic(err)
		}
	}

	// 读取文件内容
	b, err := common.ReadAllFromFile(filePath)
	if err != nil {
		panic(err)
	}

	// 加载新的json文件，是一个正常的json数组文件
	err = loadJsonFile(b, t, f)

	if err != nil {
		logs.Warn("Load json file %s error: %v", filePath, err)
		logs.Info("Load %s as obsolete json file", filePath)
		// 加载新json报错，则加载旧json
		loadObsoleteJsonFile(b, t, f)
	}
}

func loadObsoleteJsonFile(b []byte, t interface{}, f func(value interface{})) {
	// 加载旧版的json文件，以"\n"+common.ConnDataSeq分隔
	var err error
	// 根据分隔符处理内容
	for _, v := range strings.Split(string(b), "\n"+common.ConnDataSeq) {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		switch t.(type) {
		case Client:
			var client Client
			if err = json.Unmarshal([]byte(v), &client); err != nil {
				fmt.Println("Error:", err)
				return
			}
			f(&client)
			//break
		case Host:
			var host Host
			if err = json.Unmarshal([]byte(v), &host); err != nil {
				fmt.Println("Error:", err)
				return
			}
			f(&host)
			//break
		case Tunnel:
			var tunnel Tunnel
			if err = json.Unmarshal([]byte(v), &tunnel); err != nil {
				fmt.Println("Error:", err)
				return
			}
			f(&tunnel)
			//break
		}
	}
}

func loadJsonFile(b []byte, t interface{}, f func(value interface{})) error {
	// 加载新的json文件，是一个正常的json数组文件
	var err error
	switch t.(type) {
	case Client:
		var clients []Client
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
	case Host:
		var hosts []Host
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
	case Tunnel:
		var tunnels []Tunnel
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

func loadSyncMapFromFileWithSingleJson(filePath string, f func(value string)) {
	if !common.FileExists(filePath) {
		if err := createEmptyFile(filePath); err != nil {
			panic(err)
		}
		return
	}

	b, err := common.ReadAllFromFile(filePath)
	if err != nil {
		panic(err)
	}

	f(string(b))
}

// 创建空文件的辅助函数
func createEmptyFile(filePath string) error {
	dir := filepath.Dir(filePath)
	if !common.FileExists(dir) {
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			return err
		}
	}

	if !common.FileExists(filePath) {
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
		m.Range(func(key, value interface{}) bool {
			switch v := value.(type) {
			case *Tunnel:
				if v.NoStore {
					return true
				}
			case *Host:
				if v.NoStore {
					return true
				}
			case *Client:
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

func storeGlobalToFile(m *Glob, filePath string) error {
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
	return os.Rename(tmpFilePath, filePath)
}
