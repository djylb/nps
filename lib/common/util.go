package common

import (
	"bytes"
	"encoding/binary"
	"html/template"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/araddon/dateparse"
)

func Max(values ...int) int {
	maxVal := math.MinInt
	for _, v := range values {
		if v > maxVal {
			maxVal = v
		}
	}
	return maxVal
}

func Min(values ...int) int {
	minVal := math.MaxInt
	for _, v := range values {
		if v < minVal {
			minVal = v
		}
	}
	return minVal
}

// GetBoolByStr get bool by str
func GetBoolByStr(s string) bool {
	switch s {
	case "1", "true":
		return true
	}
	return false
}

// GetIntNoErrByStr int
func GetIntNoErrByStr(str string) int {
	i, _ := strconv.Atoi(strings.TrimSpace(str))
	return i
}

// GetTimeNoErrByStr time
func GetTimeNoErrByStr(str string) time.Time {
	str = strings.TrimSpace(str)
	if str == "" {
		return time.Time{}
	}
	if timestamp, err := strconv.ParseInt(str, 10, 64); err == nil {
		if timestamp > 1_000_000_000_000 {
			return time.UnixMilli(timestamp)
		}
		return time.Unix(timestamp, 0)
	}
	t, err := dateparse.ParseLocal(str)
	if err == nil {
		return t
	}
	return time.Time{}
}

func ContainsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// BytesToNum convert bytes to num
func BytesToNum(b []byte) int {
	var str string
	for i := 0; i < len(b); i++ {
		str += strconv.Itoa(int(b[i]))
	}
	x, _ := strconv.Atoi(str)
	return x
}

// TestTcpPort Judge whether the TCP port can open normally
func TestTcpPort(port int) bool {
	l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: port})
	defer func() {
		if l != nil {
			_ = l.Close()
		}
	}()
	return err == nil
}

// TestUdpPort Judge whether the UDP port can open normally
func TestUdpPort(port int) bool {
	l, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: port})
	defer func() {
		if l != nil {
			_ = l.Close()
		}
	}()
	return err == nil
}

// BinaryWrite Write length and individual byte data
// Length prevents sticking
// # Characters are used to separate data
func BinaryWrite(raw *bytes.Buffer, v ...string) {
	b := GetWriteStr(v...)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(b)))
	_, _ = raw.Write(lenBuf[:])
	_, _ = raw.Write(b)
}

// GetWriteStr get seq str
func GetWriteStr(v ...string) []byte {
	sep := ConnDataSeq
	sepLen := len(sep)
	total := 0
	for _, s := range v {
		total += len(s) + sepLen
	}
	buffer := make([]byte, 0, total)
	for _, s := range v {
		buffer = append(buffer, s...)
		buffer = append(buffer, sep...)
	}
	return buffer
}

// InStrArr inArray str interface
func InStrArr(arr []string, val string) bool {
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
}

// InIntArr inArray int interface
func InIntArr(arr []int, val int) bool {
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
}

// ParseStr parse template
func ParseStr(str string) (string, error) {
	tmp := template.New("npc")
	w := new(bytes.Buffer)
	tmp, err := tmp.Parse(str)
	if err != nil {
		return "", err
	}
	if err = tmp.Execute(w, GetEnvMap()); err != nil {
		return "", err
	}
	return w.String(), nil
}

// GetEnvMap get env
func GetEnvMap() map[string]string {
	m := make(map[string]string)
	environ := os.Environ()
	for i := range environ {
		tmp := strings.Split(environ[i], "=")
		if len(tmp) == 2 {
			m[tmp[0]] = tmp[1]
		}
	}
	return m
}

// TrimArr throw the empty element of the string array
func TrimArr(arr []string) []string {
	newArr := make([]string, 0)
	for _, v := range arr {
		trimmed := strings.TrimSpace(v)
		if trimmed != "" {
			newArr = append(newArr, trimmed)
		}
	}
	return newArr
}

func IsArrContains(arr []string, val string) bool {
	if arr == nil {
		return false
	}
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
}

// RemoveArrVal remove value from string array
func RemoveArrVal(arr []string, val string) []string {
	for k, v := range arr {
		if v == val {
			return append(arr[:k], arr[k+1:]...)
		}
	}
	return arr
}

func HandleArrEmptyVal(list []string) []string {
	for len(list) > 0 && (list[len(list)-1] == "" || strings.TrimSpace(list[len(list)-1]) == "") {
		list = list[:len(list)-1]
	}
	for i := 0; i < len(list); i++ {
		list[i] = strings.TrimSpace(list[i])
		if i > 0 && list[i] == "" {
			list[i] = list[i-1]
		}
	}
	return list
}

func ExtendArrs(arrays ...*[]string) int {
	maxLength := 0
	for _, arr := range arrays {
		if len(*arr) > maxLength {
			maxLength = len(*arr)
		}
	}
	if maxLength == 0 {
		return 0
	}
	for _, arr := range arrays {
		for len(*arr) < maxLength {
			if len(*arr) == 0 {
				*arr = append(*arr, "")
			} else {
				*arr = append(*arr, (*arr)[len(*arr)-1])
			}
		}
	}
	return maxLength
}

// GetSyncMapLen get the length of the sync map
func GetSyncMapLen(m *sync.Map) int {
	var c int
	m.Range(func(key, value interface{}) bool {
		c++
		return true
	})
	return c
}
