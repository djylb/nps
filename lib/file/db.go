package file

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/rate"
)

type DbUtils struct {
	JsonDb *JsonDb
}

var (
	Db   *DbUtils
	once sync.Once
)

// init csv from file
func GetDb() *DbUtils {
	once.Do(func() {
		jsonDb := NewJsonDb(common.GetRunPath())
		jsonDb.LoadClientFromJsonFile()
		jsonDb.LoadTaskFromJsonFile()
		jsonDb.LoadHostFromJsonFile()
		jsonDb.LoadGlobalFromJsonFile()
		Db = &DbUtils{JsonDb: jsonDb}
	})
	return Db
}

func GetMapKeys(m sync.Map, isSort bool, sortKey, order string) (keys []int) {
	if sortKey != "" && isSort {
		return sortClientByKey(m, sortKey, order)
	}
	m.Range(func(key, value interface{}) bool {
		keys = append(keys, key.(int))
		return true
	})
	sort.Ints(keys)
	return
}

func (s *DbUtils) GetClientList(start, length int, search, sort, order string, clientId int) ([]*Client, int) {
	list := make([]*Client, 0)
	var cnt int
	originLength := length
	keys := GetMapKeys(s.JsonDb.Clients, true, sort, order)
	for _, key := range keys {
		if value, ok := s.JsonDb.Clients.Load(key); ok {
			v := value.(*Client)
			if v.NoDisplay {
				continue
			}
			if clientId != 0 && clientId != v.Id {
				continue
			}
			if search != "" && !(v.Id == common.GetIntNoErrByStr(search) || common.ContainsFold(v.VerifyKey, search) || common.ContainsFold(v.Remark, search)) {
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

func (s *DbUtils) GetIdByVerifyKey(vKey string, addr string) (id int, err error) {
	var exist bool
	s.JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*Client)
		if common.Getverifyval(v.VerifyKey) == vKey && v.Status && v.Id > 0 {
			v.Addr = common.GetIpByAddr(addr)
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

func (s *DbUtils) NewTask(t *Tunnel) (err error) {
	s.JsonDb.Tasks.Range(func(key, value interface{}) bool {
		v := value.(*Tunnel)
		if (v.Mode == "secret" || v.Mode == "p2p") && v.Password == t.Password {
			err = errors.New(fmt.Sprintf("secret mode keys %s must be unique", t.Password))
			return false
		}
		return true
	})
	if err != nil {
		return
	}
	t.Flow = new(Flow)
	s.JsonDb.Tasks.Store(t.Id, t)
	s.JsonDb.StoreTasksToJsonFile()
	return
}

func (s *DbUtils) UpdateTask(t *Tunnel) error {
	s.JsonDb.Tasks.Store(t.Id, t)
	s.JsonDb.StoreTasksToJsonFile()
	return nil
}

func (s *DbUtils) SaveGlobal(t *Glob) error {
	s.JsonDb.Global = t
	s.JsonDb.StoreGlobalToJsonFile()
	return nil
}

func (s *DbUtils) DelTask(id int) error {
	s.JsonDb.Tasks.Delete(id)
	s.JsonDb.StoreTasksToJsonFile()
	return nil
}

// md5 password
func (s *DbUtils) GetTaskByMd5Password(p string) (t *Tunnel) {
	s.JsonDb.Tasks.Range(func(key, value interface{}) bool {
		if crypt.Md5(value.(*Tunnel).Password) == p {
			t = value.(*Tunnel)
			return false
		}
		return true
	})
	return
}

func (s *DbUtils) GetTask(id int) (t *Tunnel, err error) {
	if v, ok := s.JsonDb.Tasks.Load(id); ok {
		t = v.(*Tunnel)
		return
	}
	err = errors.New("not found")
	return
}

func (s *DbUtils) DelHost(id int) error {
	s.JsonDb.Hosts.Delete(id)
	s.JsonDb.StoreHostToJsonFile()
	return nil
}

func (s *DbUtils) IsHostExist(h *Host) bool {
	var exist bool
	if h.Location == "" {
		h.Location = "/"
	}
	s.JsonDb.Hosts.Range(func(key, value interface{}) bool {
		v := value.(*Host)
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

func (s *DbUtils) IsHostModify(h *Host) bool {
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
		existingHost.CertFilePath != h.CertFilePath ||
		existingHost.KeyFilePath != h.KeyFilePath {
		return true
	}

	return false
}

func (s *DbUtils) NewHost(t *Host) error {
	if t.Location == "" {
		t.Location = "/"
	}
	if s.IsHostExist(t) {
		return errors.New("host has exist")
	}
	t.Flow = new(Flow)
	s.JsonDb.Hosts.Store(t.Id, t)
	s.JsonDb.StoreHostToJsonFile()
	return nil
}

func (s *DbUtils) GetHost(start, length int, id int, search string) ([]*Host, int) {
	list := make([]*Host, 0)
	var cnt int
	originLength := length
	keys := GetMapKeys(s.JsonDb.Hosts, false, "", "")
	for _, key := range keys {
		if value, ok := s.JsonDb.Hosts.Load(key); ok {
			v := value.(*Host)
			if search != "" && !(v.Id == common.GetIntNoErrByStr(search) || common.ContainsFold(v.Host, search) || common.ContainsFold(v.Remark, search) || common.ContainsFold(v.Client.VerifyKey, search)) {
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
	s.JsonDb.Clients.Delete(id)
	s.JsonDb.StoreClientsToJsonFile()
	return nil
}

func (s *DbUtils) NewClient(c *Client) error {
	var isNotSet bool
	if c.WebUserName != "" && !s.VerifyUserName(c.WebUserName, c.Id) {
		return errors.New("web login username duplicate, please reset")
	}
reset:
	if c.VerifyKey == "" || isNotSet {
		isNotSet = true
		c.VerifyKey = crypt.GetRandomString(16, c.Id)
	}
	if c.RateLimit == 0 {
		c.Rate = rate.NewRate(int64(2 << 23))
	} else if c.Rate == nil {
		c.Rate = rate.NewRate(int64(c.RateLimit * 1024))
	}
	c.Rate.Start()
	if !s.VerifyVkey(c.VerifyKey, c.Id) {
		if isNotSet {
			goto reset
		}
		return errors.New("Vkey duplicate, please reset")
	}
	if c.Id == 0 {
		c.Id = int(s.JsonDb.GetClientId())
	}
	if c.Flow == nil {
		c.Flow = new(Flow)
	}
	s.JsonDb.Clients.Store(c.Id, c)
	s.JsonDb.StoreClientsToJsonFile()
	return nil
}

func (s *DbUtils) VerifyVkey(vkey string, id int) (res bool) {
	res = true
	s.JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*Client)
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
		v := value.(*Client)
		if v.WebUserName == username && v.Id != id {
			res = false
			return false
		}
		return true
	})
	return res
}

func (s *DbUtils) UpdateClient(t *Client) error {
	s.JsonDb.Clients.Store(t.Id, t)
	if t.RateLimit == 0 {
		t.Rate = rate.NewRate(int64(2 << 23))
		t.Rate.Start()
	}
	return nil
}

func (s *DbUtils) IsPubClient(id int) bool {
	client, err := s.GetClient(id)
	if err == nil {
		return client.NoDisplay
	}
	return false
}

func (s *DbUtils) GetClient(id int) (c *Client, err error) {
	if v, ok := s.JsonDb.Clients.Load(id); ok {
		c = v.(*Client)
		return
	}
	err = errors.New("未找到客户端")
	return
}

func (s *DbUtils) GetGlobal() (c *Glob) {
	return s.JsonDb.Global
}

func (s *DbUtils) GetClientIdByVkey(vkey string) (id int, err error) {
	var exist bool
	s.JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*Client)
		if crypt.Md5(v.VerifyKey) == vkey {
			exist = true
			id = v.Id
			return false
		}
		return true
	})
	if exist {
		return
	}
	err = errors.New("未找到客户端")
	return
}

func (s *DbUtils) GetHostById(id int) (h *Host, err error) {
	if v, ok := s.JsonDb.Hosts.Load(id); ok {
		h = v.(*Host)
		return
	}
	err = errors.New("The host could not be parsed")
	return
}

// get key by host from x
func (s *DbUtils) GetInfoByHost(host string, r *http.Request) (h *Host, err error) {
	host = common.GetIpByAddr(host)
	hostLength := len(host)

	requestPath := r.RequestURI
	if requestPath == "" {
		requestPath = "/"
	}

	scheme := r.URL.Scheme

	var bestMatch *Host
	var bestDomainLength int
	s.JsonDb.Hosts.Range(func(key, value interface{}) bool {
		v := value.(*Host)

		// 过滤无效项
		if v.IsClose || (v.Scheme != "all" && v.Scheme != scheme) {
			return true
		}

		curDomainLength := len(strings.TrimPrefix(v.Host, "*"))
		if hostLength < curDomainLength {
			return true
		}

		// 匹配域名
		matched := false
		if v.Host == host {
			matched = true
		} else if strings.HasPrefix(v.Host, "*") && strings.HasSuffix(host, v.Host[1:]) {
			matched = true
		}
		if !matched {
			return true
		}

		// 匹配路径，空 Location 默认 "/"
		location := v.Location
		if location == "" {
			location = "/"
		}

		// 请求路径不匹配则跳过
		if !strings.HasPrefix(requestPath, location) {
			return true
		}

		// 更新最佳匹配项：
		// 1. 域名长度（去除通配符）越长优先级越高
		// 2. 相同长度时，Location（路径）的长度越长优先级越高
		// 3. 若相同，则优先匹配完全一致的域名
		if bestMatch == nil {
			bestMatch = v
			bestDomainLength = len(strings.TrimPrefix(bestMatch.Host, "*"))
		} else {
			if curDomainLength > bestDomainLength {
				bestMatch = v
				bestDomainLength = len(strings.TrimPrefix(bestMatch.Host, "*"))
			} else if curDomainLength == bestDomainLength {
				if len(location) > len(bestMatch.Location) {
					bestMatch = v
					bestDomainLength = len(strings.TrimPrefix(bestMatch.Host, "*"))
				} else if len(location) == len(bestMatch.Location) && !strings.HasPrefix(v.Host, "*") && strings.HasPrefix(bestMatch.Host, "*") {
					bestMatch = v
					bestDomainLength = len(strings.TrimPrefix(bestMatch.Host, "*"))
				}
			}
		}
		return true
	})

	if bestMatch != nil {
		return bestMatch, nil
	}
	return nil, errors.New("The host could not be parsed")
}

func (s *DbUtils) FindCertByHost(host string) (*Host, error) {
	if host == "" {
		return nil, errors.New("Invalid Host")
	}

	hostLength := len(host)

	var bestMatch *Host
	var bestDomainLength int
	s.JsonDb.Hosts.Range(func(key, value interface{}) bool {
		v := value.(*Host)

		// 过滤无效项
		if v.IsClose || (v.Scheme == "http") {
			return true
		}

		curDomainLength := len(strings.TrimPrefix(v.Host, "*"))
		if hostLength < curDomainLength {
			return true
		}

		// 匹配域名
		matched := false
		if v.Host == host {
			if v.Location == "/" || v.Location == "" {
				bestMatch = v
				return false
			}
			matched = true
		} else if strings.HasPrefix(v.Host, "*") && strings.HasSuffix(host, v.Host[1:]) {
			matched = true
		}
		if !matched {
			return true
		}

		if bestMatch == nil {
			bestMatch = v
			bestDomainLength = len(strings.TrimPrefix(bestMatch.Host, "*"))
		} else {
			if curDomainLength > bestDomainLength {
				bestMatch = v
				bestDomainLength = len(strings.TrimPrefix(bestMatch.Host, "*"))
			} else if curDomainLength == bestDomainLength {
				if !strings.HasPrefix(v.Host, "*") && strings.HasPrefix(bestMatch.Host, "*") {
					bestMatch = v
					bestDomainLength = len(strings.TrimPrefix(bestMatch.Host, "*"))
				} else if len(v.Location) < len(bestMatch.Location) {
					bestMatch = v
					bestDomainLength = len(strings.TrimPrefix(bestMatch.Host, "*"))
				} else if v.Location == "/" || v.Location == "" {
					bestMatch = v
					bestDomainLength = len(strings.TrimPrefix(bestMatch.Host, "*"))
				}
			}
		}
		return true
	})

	if bestMatch != nil {
		return bestMatch, nil
	}
	return nil, errors.New("The host could not be parsed")
}
