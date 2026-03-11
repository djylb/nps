package server

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/beego/beego"
	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/version"
)

func GetTunnel(start, length int, typeVal string, clientId int, search string, sortField string, order string) ([]*file.Tunnel, int) {
	allList := make([]*file.Tunnel, 0) //store all Tunnel
	list := make([]*file.Tunnel, 0)
	originLength := length
	var cnt int

	keys := file.GetMapKeys(&file.GetDb().JsonDb.Tasks, false, "", "")

	// get all Tunnel and filter
	for _, key := range keys {
		if value, ok := file.GetDb().JsonDb.Tasks.Load(key); ok {
			v := value.(*file.Tunnel)
			if (typeVal != "" && v.Mode != typeVal || (clientId != 0 && v.Client.Id != clientId)) || (typeVal == "" && clientId != v.Client.Id) {
				continue
			}
			allList = append(allList, v)
		}
	}

	// sort by field, asc or desc
	switch sortField {
	case "Id":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Id < allList[j].Id })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Id > allList[j].Id })
		}

	case "Client.Id":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Client.Id < allList[j].Client.Id })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Client.Id > allList[j].Client.Id })
		}

	case "Remark":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Remark < allList[j].Remark })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Remark > allList[j].Remark })
		}

	case "Client.VerifyKey":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Client.VerifyKey < allList[j].Client.VerifyKey })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Client.VerifyKey > allList[j].Client.VerifyKey })
		}

	case "Target.TargetStr":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Target.TargetStr < allList[j].Target.TargetStr })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Target.TargetStr > allList[j].Target.TargetStr })
		}

	case "Port":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Port < allList[j].Port })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Port > allList[j].Port })
		}

	case "Mode":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Mode < allList[j].Mode })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Mode > allList[j].Mode })
		}

	case "TargetType":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].TargetType < allList[j].TargetType })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].TargetType > allList[j].TargetType })
		}

	case "Password":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Password < allList[j].Password })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Password > allList[j].Password })
		}

	case "HttpProxy":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].HttpProxy && !allList[j].HttpProxy })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return !allList[i].HttpProxy && allList[j].HttpProxy })
		}

	case "Socks5Proxy":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Socks5Proxy && !allList[j].Socks5Proxy })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return !allList[i].Socks5Proxy && allList[j].Socks5Proxy })
		}

	case "NowConn":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].NowConn < allList[j].NowConn })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].NowConn > allList[j].NowConn })
		}

	case "InletFlow":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Flow.InletFlow < allList[j].Flow.InletFlow })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Flow.InletFlow > allList[j].Flow.InletFlow })
		}

	case "ExportFlow":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Flow.ExportFlow < allList[j].Flow.ExportFlow })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Flow.ExportFlow > allList[j].Flow.ExportFlow })
		}

	case "TotalFlow":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool {
				return allList[i].Flow.InletFlow+allList[i].Flow.ExportFlow < allList[j].Flow.InletFlow+allList[j].Flow.ExportFlow
			})
		} else {
			sort.SliceStable(allList, func(i, j int) bool {
				return allList[i].Flow.InletFlow+allList[i].Flow.ExportFlow > allList[j].Flow.InletFlow+allList[j].Flow.ExportFlow
			})
		}

	case "FlowRemain":
		asc := order == "asc"
		const mb = int64(1024 * 1024)
		rem := func(f *file.Flow) int64 {
			if f.FlowLimit == 0 {
				if asc {
					return math.MaxInt64
				}
				return math.MinInt64
			}
			return f.FlowLimit*mb - (f.InletFlow + f.ExportFlow)
		}
		sort.SliceStable(allList, func(i, j int) bool {
			ri, rj := rem(allList[i].Flow), rem(allList[j].Flow)
			if asc {
				return ri < rj
			}
			return ri > rj
		})

	case "Flow.FlowLimit":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool {
				vi, vj := allList[i].Flow.FlowLimit, allList[j].Flow.FlowLimit
				return (vi != 0 && vj == 0) || (vi != 0 && vj != 0 && vi < vj)
			})
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Flow.FlowLimit > allList[j].Flow.FlowLimit })
		}

	case "Flow.TimeLimit", "TimeRemain":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool {
				ti, tj := allList[i].Flow.TimeLimit, allList[j].Flow.TimeLimit
				return (!ti.IsZero() && tj.IsZero()) || (!ti.IsZero() && !tj.IsZero() && ti.Before(tj))
			})
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Flow.TimeLimit.After(allList[j].Flow.TimeLimit) })
		}

	case "Status":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Status && !allList[j].Status })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return !allList[i].Status && allList[j].Status })
		}

	case "RunStatus":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].RunStatus && !allList[j].RunStatus })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return !allList[i].RunStatus && allList[j].RunStatus })
		}

	case "Client.IsConnect":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].Client.IsConnect && !allList[j].Client.IsConnect })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return !allList[i].Client.IsConnect && allList[j].Client.IsConnect })
		}
	}

	searchInt := common.GetIntNoErrByStr(search)

	// search + paging
	for _, v := range allList {
		if search != "" &&
			v.Id != searchInt &&
			v.Port != searchInt &&
			!common.ContainsFold(v.Password, search) &&
			!common.ContainsFold(v.Remark, search) &&
			!common.ContainsFold(v.Target.TargetStr, search) {
			continue
		}

		cnt++

		if _, ok := Bridge.Client.Load(v.Client.Id); ok {
			v.Client.IsConnect = true
		} else {
			v.Client.IsConnect = false
		}

		if _, ok := RunList.Load(v.Id); ok {
			v.RunStatus = true
		} else {
			v.RunStatus = false
		}

		if start--; start < 0 {
			if originLength == 0 {
				list = append(list, v)
			} else if length--; length >= 0 {
				list = append(list, v)
			}
		}
	}

	return list, cnt
}

// GetHostList get client list
func GetHostList(start, length, clientId int, search, sortField, order string) (list []*file.Host, cnt int) {
	list, cnt = file.GetDb().GetHost(start, length, clientId, search)

	// sort by field, asc or desc
	switch sortField {
	case "Id":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Id < list[j].Id })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Id > list[j].Id })
		}

	case "Client.Id":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Client.Id < list[j].Client.Id })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Client.Id > list[j].Client.Id })
		}

	case "Remark":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Remark < list[j].Remark })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Remark > list[j].Remark })
		}

	case "Client.VerifyKey":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Client.VerifyKey < list[j].Client.VerifyKey })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Client.VerifyKey > list[j].Client.VerifyKey })
		}

	case "Host":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Host < list[j].Host })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Host > list[j].Host })
		}

	case "Scheme":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Scheme < list[j].Scheme })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Scheme > list[j].Scheme })
		}

	case "TargetIsHttps":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].TargetIsHttps && !list[j].TargetIsHttps })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].TargetIsHttps && list[j].TargetIsHttps })
		}

	case "Target.TargetStr":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Target.TargetStr < list[j].Target.TargetStr })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Target.TargetStr > list[j].Target.TargetStr })
		}

	case "Location":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Location < list[j].Location })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Location > list[j].Location })
		}

	case "PathRewrite":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].PathRewrite < list[j].PathRewrite })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].PathRewrite > list[j].PathRewrite })
		}

	case "CertType":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].CertType < list[j].CertType })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].CertType > list[j].CertType })
		}

	case "AutoSSL":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].AutoSSL && !list[j].AutoSSL })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].AutoSSL && list[j].AutoSSL })
		}

	case "AutoHttps":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].AutoHttps && !list[j].AutoHttps })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].AutoHttps && list[j].AutoHttps })
		}

	case "AutoCORS":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].AutoCORS && !list[j].AutoCORS })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].AutoCORS && list[j].AutoCORS })
		}

	case "CompatMode":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].CompatMode && !list[j].CompatMode })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].CompatMode && list[j].CompatMode })
		}

	case "HttpsJustProxy":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].HttpsJustProxy && !list[j].HttpsJustProxy })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].HttpsJustProxy && list[j].HttpsJustProxy })
		}

	case "TlsOffload":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].TlsOffload && !list[j].TlsOffload })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].TlsOffload && list[j].TlsOffload })
		}

	case "NowConn":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].NowConn < list[j].NowConn })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].NowConn > list[j].NowConn })
		}

	case "InletFlow":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.InletFlow < list[j].Flow.InletFlow })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.InletFlow > list[j].Flow.InletFlow })
		}

	case "ExportFlow":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.ExportFlow < list[j].Flow.ExportFlow })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.ExportFlow > list[j].Flow.ExportFlow })
		}

	case "TotalFlow":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				return list[i].Flow.InletFlow+list[i].Flow.ExportFlow < list[j].Flow.InletFlow+list[j].Flow.ExportFlow
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool {
				return list[i].Flow.InletFlow+list[i].Flow.ExportFlow > list[j].Flow.InletFlow+list[j].Flow.ExportFlow
			})
		}

	case "FlowRemain":
		asc := order == "asc"
		const mb = int64(1024 * 1024)
		rem := func(f *file.Flow) int64 {
			if f.FlowLimit == 0 {
				if asc {
					return math.MaxInt64
				}
				return math.MinInt64
			}
			return f.FlowLimit*mb - (f.InletFlow + f.ExportFlow)
		}
		sort.SliceStable(list, func(i, j int) bool {
			ri, rj := rem(list[i].Flow), rem(list[j].Flow)
			if asc {
				return ri < rj
			}
			return ri > rj
		})

	case "Flow.FlowLimit":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				vi, vj := list[i].Flow.FlowLimit, list[j].Flow.FlowLimit
				return (vi != 0 && vj == 0) || (vi != 0 && vj != 0 && vi < vj)
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.FlowLimit > list[j].Flow.FlowLimit })
		}

	case "Flow.TimeLimit", "TimeRemain":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				ti, tj := list[i].Flow.TimeLimit, list[j].Flow.TimeLimit
				return (!ti.IsZero() && tj.IsZero()) || (!ti.IsZero() && !tj.IsZero() && ti.Before(tj))
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.TimeLimit.After(list[j].Flow.TimeLimit) })
		}

	case "IsClose":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].IsClose && !list[j].IsClose })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].IsClose && list[j].IsClose })
		}

	case "Client.IsConnect":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Client.IsConnect && !list[j].Client.IsConnect })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].Client.IsConnect && list[j].Client.IsConnect })
		}
	}

	return
}

// GetClientList get client list
func GetClientList(start, length int, search, sortField, order string, clientId int) (list []*file.Client, cnt int) {
	list, cnt = file.GetDb().GetClientList(start, length, search, sortField, order, clientId)

	// sort by Id, Remark, Port..., asc or desc
	switch sortField {
	case "Id":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Id < list[j].Id })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Id > list[j].Id })
		}

	case "Addr":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Addr < list[j].Addr })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Addr > list[j].Addr })
		}

	case "LocalAddr":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].LocalAddr < list[j].LocalAddr })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].LocalAddr > list[j].LocalAddr })
		}

	case "Remark":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Remark < list[j].Remark })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Remark > list[j].Remark })
		}

	case "VerifyKey":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].VerifyKey < list[j].VerifyKey })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].VerifyKey > list[j].VerifyKey })
		}

	case "TotalFlow":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				return list[i].Flow.InletFlow+list[i].Flow.ExportFlow < list[j].Flow.InletFlow+list[j].Flow.ExportFlow
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool {
				return list[i].Flow.InletFlow+list[i].Flow.ExportFlow > list[j].Flow.InletFlow+list[j].Flow.ExportFlow
			})
		}

	case "FlowRemain":
		asc := order == "asc"
		const mb = int64(1024 * 1024)
		rem := func(f *file.Flow) int64 {
			if f.FlowLimit == 0 {
				if asc {
					return math.MaxInt64
				}
				return math.MinInt64
			}
			return f.FlowLimit*mb - (f.InletFlow + f.ExportFlow)
		}
		sort.SliceStable(list, func(i, j int) bool {
			ri, rj := rem(list[i].Flow), rem(list[j].Flow)
			if asc {
				return ri < rj
			}
			return ri > rj
		})

	case "NowConn":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].NowConn < list[j].NowConn })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].NowConn > list[j].NowConn })
		}

	case "Version":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Version < list[j].Version })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Version > list[j].Version })
		}

	case "Mode":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Mode < list[j].Mode })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Mode > list[j].Mode })
		}

	case "Rate.NowRate":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Rate.Now() < list[j].Rate.Now() })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Rate.Now() > list[j].Rate.Now() })
		}

	case "Flow.FlowLimit":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				vi, vj := list[i].Flow.FlowLimit, list[j].Flow.FlowLimit
				return (vi != 0 && vj == 0) || (vi != 0 && vj != 0 && vi < vj)
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.FlowLimit > list[j].Flow.FlowLimit })
		}

	case "Flow.TimeLimit", "TimeRemain":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				ti, tj := list[i].Flow.TimeLimit, list[j].Flow.TimeLimit
				return (!ti.IsZero() && tj.IsZero()) || (!ti.IsZero() && !tj.IsZero() && ti.Before(tj))
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Flow.TimeLimit.After(list[j].Flow.TimeLimit) })
		}

	case "Status":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Status && !list[j].Status })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].Status && list[j].Status })
		}

	case "IsConnect":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].IsConnect && !list[j].IsConnect })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return !list[i].IsConnect && list[j].IsConnect })
		}
	}

	dealClientData()
	return
}

func dealClientData() {
	//logs.Info("dealClientData.........")
	file.GetDb().JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*file.Client)
		if vv, ok := Bridge.Client.Load(v.Id); ok {
			v.IsConnect = true
			v.LastOnlineTime = time.Now().Format("2006-01-02 15:04:05")
			cli := vv.(*bridge.Client)
			node, ok := cli.GetNodeByUUID(cli.LastUUID)
			var ver string
			if ok {
				ver = node.Version
			}
			count := cli.NodeCount()
			if count > 1 {
				ver = fmt.Sprintf("%s(%d)", ver, cli.NodeCount())
			}
			v.Version = ver
		} else if v.Id <= 0 {
			if allowLocalProxy, _ := beego.AppConfig.Bool("allow_local_proxy"); allowLocalProxy {
				v.IsConnect = v.Status
				v.Version = version.VERSION
				v.Mode = "local"
				v.LocalAddr = common.GetOutboundIP().String()
				// Add Local Client
				if _, exists := Bridge.Client.Load(v.Id); !exists && v.Status {
					Bridge.Client.Store(v.Id, bridge.NewClient(v.Id, bridge.NewNode("127.0.0.1", version.VERSION, version.GetLatestIndex())))
					logs.Debug("Inserted virtual client for ID %d", v.Id)
				}
			} else {
				v.IsConnect = false
			}
		} else {
			v.IsConnect = false
		}
		v.InletFlow = 0
		v.ExportFlow = 0
		return true
	})
	file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
		h := value.(*file.Host)
		c, err := file.GetDb().GetClient(h.Client.Id)
		if err != nil {
			return true
		}
		c.InletFlow += h.Flow.InletFlow
		c.ExportFlow += h.Flow.ExportFlow
		return true
	})
	file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		t := value.(*file.Tunnel)
		c, err := file.GetDb().GetClient(t.Client.Id)
		if err != nil {
			return true
		}
		c.InletFlow += t.Flow.InletFlow
		c.ExportFlow += t.Flow.ExportFlow
		return true
	})
	//return
}
