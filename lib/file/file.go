package file

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/beego/beego"
	"github.com/beego/beego/logs"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/lib/version"
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
	loadSyncMapFromFile(s.TaskFilePath, Tunnel{}, func(v interface{}) {
		var err error
		post := v.(*Tunnel)
		if post.Client, err = s.GetClient(post.Client.Id); err != nil {
			return
		}
		s.Tasks.Store(post.Id, post)
		if post.Id > int(s.TaskIncreaseId) {
			s.TaskIncreaseId = int32(post.Id)
		}
	})
}

func (s *JsonDb) LoadClientFromJsonFile() {
	if allowLocalProxy, _ := beego.AppConfig.Bool("allow_local_proxy"); allowLocalProxy {
		if _, err := s.GetClient(-1); err != nil {
			local := new(Client)
			local.Id = -1
			local.Remark = "Local Proxy"
			local.Addr = "127.0.0.1"
			local.Cnf = new(Config)
			local.Flow = new(Flow)
			local.Rate = new(rate.Rate)
			local.Status = true
			local.ConfigConnAllow = true
			local.Version = version.VERSION
			local.VerifyKey = "localproxy"
			s.Clients.Store(local.Id, local)
			s.ClientIncreaseId = 0
			logs.Notice("Auto create local proxy client.")
		}
	}
	loadSyncMapFromFile(s.ClientFilePath, Client{}, func(v interface{}) {
		post := v.(*Client)
		if post.RateLimit > 0 {
			post.Rate = rate.NewRate(int64(post.RateLimit * 1024))
		} else {
			post.Rate = rate.NewRate(int64(2 << 23))
		}
		post.Rate.Start()
		post.NowConn = 0
		s.Clients.Store(post.Id, post)
		if post.Id > int(s.ClientIncreaseId) {
			s.ClientIncreaseId = int32(post.Id)
		}
	})
}

func (s *JsonDb) LoadHostFromJsonFile() {
	loadSyncMapFromFile(s.HostFilePath, Host{}, func(v interface{}) {
		var err error
		post := v.(*Host)
		if post.Client, err = s.GetClient(post.Client.Id); err != nil {
			return
		}
		s.Hosts.Store(post.Id, post)
		if post.Id > int(s.HostIncreaseId) {
			s.HostIncreaseId = int32(post.Id)
		}
	})
}

func (s *JsonDb) LoadGlobalFromJsonFile() {
	loadSyncMapFromFileWithSingleJson(s.GlobalFilePath, func(v string) {
		post := new(Glob)
		if json.Unmarshal([]byte(v), &post) != nil {
			return
		}
		s.Global = post
	})
}

func (s *JsonDb) GetClient(id int) (c *Client, err error) {
	if v, ok := s.Clients.Load(id); ok {
		c = v.(*Client)
		return
	}
	err = errors.New("未找到客户端")
	return
}

var hostLock sync.Mutex

func (s *JsonDb) StoreHostToJsonFile() {
	hostLock.Lock()
	storeSyncMapToFile(s.Hosts, s.HostFilePath)
	hostLock.Unlock()
}

var taskLock sync.Mutex

func (s *JsonDb) StoreTasksToJsonFile() {
	taskLock.Lock()
	storeSyncMapToFile(s.Tasks, s.TaskFilePath)
	taskLock.Unlock()
}

var clientLock sync.Mutex

func (s *JsonDb) StoreClientsToJsonFile() {
	clientLock.Lock()
	storeSyncMapToFile(s.Clients, s.ClientFilePath)
	clientLock.Unlock()
}

var globalLock sync.Mutex

func (s *JsonDb) StoreGlobalToJsonFile() {
	globalLock.Lock()
	storeGlobalToFile(s.Global, s.GlobalFilePath)
	globalLock.Unlock()
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

	if strings.Contains(string(b), "\n"+common.CONN_DATA_SEQ) {
		// json文件里面，存在 "\n"+common.CONN_DATA_SEQ ，说明是旧的json
		loadObsoleteJsonFile(b, t, f)
		return
	}

	// 加载新的json文件，是一个正常的json数组文件
	loadJsonFile(b, t, f)
}

func loadObsoleteJsonFile(b []byte, t interface{}, f func(value interface{})) {
	// 加载旧版的json文件，以"\n"+common.CONN_DATA_SEQ分隔
	var err error
	// 根据分隔符处理内容
	for _, v := range strings.Split(string(b), "\n"+common.CONN_DATA_SEQ) {
		switch t.(type) {
		case Client:
			var client Client
			if len(b) != 0 {
				err = json.Unmarshal([]byte(v), &client)
				if err != nil {
					fmt.Println("Error:", err)
					return
				}
			}

			f(&client)
			break
		case Host:
			var host Host
			if len(b) != 0 {
				err = json.Unmarshal([]byte(v), &host)
				if err != nil {
					fmt.Println("Error:", err)
					return
				}
			}

			f(&host)
			break
		case Tunnel:
			var tunnel Tunnel
			if len(b) != 0 {
				err = json.Unmarshal([]byte(v), &tunnel)
				if err != nil {
					fmt.Println("Error:", err)
					return
				}
			}

			f(&tunnel)
			break
		}
	}
}

func loadJsonFile(b []byte, t interface{}, f func(value interface{})) {
	// 加载新的json文件，是一个正常的json数组文件
	var err error
	switch t.(type) {
	case Client:
		var clients []Client
		if len(b) != 0 {
			err = json.Unmarshal(b, &clients)
			if err != nil {
				fmt.Println("Error:", err)
				return
			}
		}

		for i := range clients {
			f(&clients[i])
		}
		break
	case Host:
		var hosts []Host
		if len(b) != 0 {
			err = json.Unmarshal(b, &hosts)
			if err != nil {
				fmt.Println("Error:", err)
				return
			}
		}

		for i := range hosts {
			f(&hosts[i])
		}
		break
	case Tunnel:
		var tunnels []Tunnel
		if len(b) != 0 {
			err = json.Unmarshal(b, &tunnels)
			if err != nil {
				fmt.Println("Error:", err)
				return
			}
		}

		for i := range tunnels {
			f(&tunnels[i])
		}
		break
	}
}

func loadSyncMapFromFileWithSingleJson(filePath string, f func(value string)) {
	// 如果文件不存在，则创建空文件
	if !common.FileExists(filePath) {
		if err := createEmptyFile(filePath); err != nil {
			panic(err)
		}
		return
	}

	// 读取文件内容
	b, err := common.ReadAllFromFile(filePath)
	if err != nil {
		panic(err)
	}

	f(string(b))
}

// 创建空文件的辅助函数
func createEmptyFile(filePath string) error {
	// 确保目录存在
	dir := filepath.Dir(filePath)
	if !common.FileExists(dir) {
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			return err
		}
	}

	// 如果文件不存在，则创建空文件
	if !common.FileExists(filePath) {
		file, err := os.Create(filePath)
		if err != nil {
			return err
		}
		defer file.Close() // 创建后立即关闭
	}

	return nil
}

func storeSyncMapToFile(m sync.Map, filePath string) {
	file, err := os.Create(filePath + ".tmp")
	// first create a temporary file to store
	if err != nil {
		panic(err)
	}

	var index = 0
	var total = 0

	m.Range(func(key, value interface{}) bool {
		total++
		return true
	})

	// json数组，以“[”开头，然后换行，一行一条json数据
	_, _ = file.WriteString("[\n")
	m.Range(func(key, value interface{}) bool {
		index++
		var b []byte
		var err error
		switch value.(type) {
		case *Tunnel:
			obj := value.(*Tunnel)
			if obj.NoStore {
				return true
			}
			b, err = json.Marshal(obj)
		case *Host:
			obj := value.(*Host)
			if obj.NoStore {
				return true
			}
			b, err = json.Marshal(obj)
		case *Client:
			obj := value.(*Client)
			if obj.NoStore {
				return true
			}
			b, err = json.Marshal(obj)
		//case *Glob:
		//	obj := value.(*Glob)
		//	b, err = json.Marshal(obj)
		default:
			return true
		}

		if err != nil {
			return true
		}

		// 每一条json数据前加空格
		_, _ = file.WriteString("  ")

		// 写入一条json数据
		_, err = file.Write(b)
		if err != nil {
			panic(err)
		}

		if index >= 1 && index <= total-1 {
			// 不是第一条和最后一条时，每一条json数据后加逗号，同时换行
			_, _ = file.Write([]byte(",\n"))
		}
		return true
	})
	// json数组，“]”结束，结束前需要换行
	_, _ = file.WriteString("\n]")

	_ = file.Sync()
	_ = file.Close()

	// must close file first, then rename it
	err = os.Rename(filePath+".tmp", filePath)
	if err != nil {
		logs.Error(err, "store to file err, data will lost")
	}
	// replace the file, maybe provides atomic operation
}

func storeGlobalToFile(m *Glob, filePath string) {
	file, err := os.Create(filePath + ".tmp")
	// first create a temporary file to store
	if err != nil {
		panic(err)
	}

	var b []byte
	b, err = json.Marshal(m)
	_, err = file.Write(b)
	if err != nil {
		panic(err)
	}
	_ = file.Sync()
	_ = file.Close()
	// must close file first, then rename it
	err = os.Rename(filePath+".tmp", filePath)
	if err != nil {
		logs.Error(err, "store to file err, data will lost")
	}
}
