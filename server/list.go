package server

import (
	"math"
	"sort"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
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
			v, ok := value.(*file.Tunnel)
			if !ok || v == nil || v.Client == nil || v.Target == nil {
				continue
			}
			if (typeVal != "" && v.Mode != typeVal || (clientId != 0 && v.Client.Id != clientId)) || (typeVal == "" && clientId != v.Client.Id) {
				continue
			}
			allList = append(allList, v)
		}
	}

	fv := flowSnapshots(allList, func(t *file.Tunnel) *file.Flow { return t.Flow })

	// sort by field, asc or desc
	switch sortField {
	case "speed", "upload_rate", "download_rate", "Speed", "UploadRate", "DownloadRate":
		sortCurrentRates(allList, sortField, order)

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
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].CurrentConnections() < allList[j].CurrentConnections() })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return allList[i].CurrentConnections() > allList[j].CurrentConnections() })
		}

	case "InletFlow":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return fv[allList[i].Flow].InletFlow < fv[allList[j].Flow].InletFlow })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return fv[allList[i].Flow].InletFlow > fv[allList[j].Flow].InletFlow })
		}

	case "ExportFlow":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool { return fv[allList[i].Flow].ExportFlow < fv[allList[j].Flow].ExportFlow })
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return fv[allList[i].Flow].ExportFlow > fv[allList[j].Flow].ExportFlow })
		}

	case "TotalFlow", "ActualFlow": // the actual usage is twice the total
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool {
				return fv[allList[i].Flow].InletFlow+fv[allList[i].Flow].ExportFlow < fv[allList[j].Flow].InletFlow+fv[allList[j].Flow].ExportFlow
			})
		} else {
			sort.SliceStable(allList, func(i, j int) bool {
				return fv[allList[i].Flow].InletFlow+fv[allList[i].Flow].ExportFlow > fv[allList[j].Flow].InletFlow+fv[allList[j].Flow].ExportFlow
			})
		}

	case "FlowRemain":
		asc := order == "asc"
		const mb = int64(1024 * 1024)
		rem := func(f file.FlowValues) int64 {
			if f.FlowLimit == 0 {
				if asc {
					return math.MaxInt64
				}
				return math.MinInt64
			}
			return f.FlowLimit*mb - f.Used
		}
		sort.SliceStable(allList, func(i, j int) bool {
			ri, rj := rem(fv[allList[i].Flow]), rem(fv[allList[j].Flow])
			if asc {
				return ri < rj
			}
			return ri > rj
		})

	case "Flow.FlowLimit":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool {
				vi, vj := fv[allList[i].Flow].FlowLimit, fv[allList[j].Flow].FlowLimit
				return (vi != 0 && vj == 0) || (vi != 0 && vj != 0 && vi < vj)
			})
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return fv[allList[i].Flow].FlowLimit > fv[allList[j].Flow].FlowLimit })
		}

	case "Flow.TimeLimit", "TimeRemain":
		if order == "asc" {
			sort.SliceStable(allList, func(i, j int) bool {
				ti, tj := fv[allList[i].Flow].TimeLimit, fv[allList[j].Flow].TimeLimit
				return (!ti.IsZero() && tj.IsZero()) || (!ti.IsZero() && !tj.IsZero() && ti.Before(tj))
			})
		} else {
			sort.SliceStable(allList, func(i, j int) bool { return fv[allList[i].Flow].TimeLimit.After(fv[allList[j].Flow].TimeLimit) })
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
	// all the matching hosts are sorted before a page is taken
	list, cnt = file.GetDb().GetHost(0, 0, clientId, search)
	defer func() { list = page(list, start, length) }()
	fv := flowSnapshots(list, func(h *file.Host) *file.Flow { return h.Flow })

	// sort by field, asc or desc
	switch sortField {
	case "speed", "upload_rate", "download_rate", "Speed", "UploadRate", "DownloadRate":
		sortCurrentRates(list, sortField, order)

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
			sort.SliceStable(list, func(i, j int) bool { return list[i].CurrentConnections() < list[j].CurrentConnections() })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].CurrentConnections() > list[j].CurrentConnections() })
		}

	case "InletFlow":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return fv[list[i].Flow].InletFlow < fv[list[j].Flow].InletFlow })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return fv[list[i].Flow].InletFlow > fv[list[j].Flow].InletFlow })
		}

	case "ExportFlow":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return fv[list[i].Flow].ExportFlow < fv[list[j].Flow].ExportFlow })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return fv[list[i].Flow].ExportFlow > fv[list[j].Flow].ExportFlow })
		}

	case "TotalFlow", "ActualFlow": // the actual usage is twice the total
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				return fv[list[i].Flow].InletFlow+fv[list[i].Flow].ExportFlow < fv[list[j].Flow].InletFlow+fv[list[j].Flow].ExportFlow
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool {
				return fv[list[i].Flow].InletFlow+fv[list[i].Flow].ExportFlow > fv[list[j].Flow].InletFlow+fv[list[j].Flow].ExportFlow
			})
		}

	case "FlowRemain":
		asc := order == "asc"
		const mb = int64(1024 * 1024)
		rem := func(f file.FlowValues) int64 {
			if f.FlowLimit == 0 {
				if asc {
					return math.MaxInt64
				}
				return math.MinInt64
			}
			return f.FlowLimit*mb - f.Used
		}
		sort.SliceStable(list, func(i, j int) bool {
			ri, rj := rem(fv[list[i].Flow]), rem(fv[list[j].Flow])
			if asc {
				return ri < rj
			}
			return ri > rj
		})

	case "Flow.FlowLimit":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				vi, vj := fv[list[i].Flow].FlowLimit, fv[list[j].Flow].FlowLimit
				return (vi != 0 && vj == 0) || (vi != 0 && vj != 0 && vi < vj)
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool { return fv[list[i].Flow].FlowLimit > fv[list[j].Flow].FlowLimit })
		}

	case "Flow.TimeLimit", "TimeRemain":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				ti, tj := fv[list[i].Flow].TimeLimit, fv[list[j].Flow].TimeLimit
				return (!ti.IsZero() && tj.IsZero()) || (!ti.IsZero() && !tj.IsZero() && ti.Before(tj))
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool { return fv[list[i].Flow].TimeLimit.After(fv[list[j].Flow].TimeLimit) })
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
	// all the matching clients are sorted before a page is taken
	list, cnt = file.GetDb().GetClientList(0, 0, search, sortField, order, clientId)
	defer func() { list = page(list, start, length) }()
	fv := flowSnapshots(list, func(c *file.Client) *file.Flow { return c.Flow })

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

	case "TotalFlow", "ActualFlow": // the actual usage is twice the total
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				return fv[list[i].Flow].InletFlow+fv[list[i].Flow].ExportFlow < fv[list[j].Flow].InletFlow+fv[list[j].Flow].ExportFlow
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool {
				return fv[list[i].Flow].InletFlow+fv[list[i].Flow].ExportFlow > fv[list[j].Flow].InletFlow+fv[list[j].Flow].ExportFlow
			})
		}

	case "FlowRemain":
		asc := order == "asc"
		const mb = int64(1024 * 1024)
		rem := func(f file.FlowValues) int64 {
			if f.FlowLimit == 0 {
				if asc {
					return math.MaxInt64
				}
				return math.MinInt64
			}
			return f.FlowLimit*mb - f.Used
		}
		sort.SliceStable(list, func(i, j int) bool {
			ri, rj := rem(fv[list[i].Flow]), rem(fv[list[j].Flow])
			if asc {
				return ri < rj
			}
			return ri > rj
		})

	case "NowConn":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool { return list[i].CurrentConnections() < list[j].CurrentConnections() })
		} else {
			sort.SliceStable(list, func(i, j int) bool { return list[i].CurrentConnections() > list[j].CurrentConnections() })
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
				vi, vj := fv[list[i].Flow].FlowLimit, fv[list[j].Flow].FlowLimit
				return (vi != 0 && vj == 0) || (vi != 0 && vj != 0 && vi < vj)
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool { return fv[list[i].Flow].FlowLimit > fv[list[j].Flow].FlowLimit })
		}

	case "Flow.TimeLimit", "TimeRemain":
		if order == "asc" {
			sort.SliceStable(list, func(i, j int) bool {
				ti, tj := fv[list[i].Flow].TimeLimit, fv[list[j].Flow].TimeLimit
				return (!ti.IsZero() && tj.IsZero()) || (!ti.IsZero() && !tj.IsZero() && ti.Before(tj))
			})
		} else {
			sort.SliceStable(list, func(i, j int) bool { return fv[list[i].Flow].TimeLimit.After(fv[list[j].Flow].TimeLimit) })
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

// flowSnapshots takes a snapshot of the flow of every entry of list, so that
// sorting reads consistent values under no lock; a nil flow reads as zero.
func flowSnapshots[T any](list []T, flow func(T) *file.Flow) map[*file.Flow]file.FlowValues {
	fv := make(map[*file.Flow]file.FlowValues, len(list))
	for _, v := range list {
		if f := flow(v); f != nil {
			fv[f] = f.Values()
		}
	}
	return fv
}

// page returns the page of length entries of list from start; a length of
// 0 or less returns the entries from start on.
func page[T any](list []T, start, length int) []T {
	start = max(start, 0)
	if start >= len(list) {
		return list[:0]
	}
	list = list[start:]
	if length > 0 && length < len(list) {
		list = list[:length]
	}
	return list
}

// Read current rates once so a changing meter cannot invalidate the comparator.
func sortCurrentRates[T interface{ CurrentRates() (int64, int64) }](rows []T, field, order string) {
	type entry struct {
		row   T
		value int64
	}
	values := make([]entry, len(rows))
	for i, row := range rows {
		upload, download := row.CurrentRates()
		value := upload + download
		switch field {
		case "upload_rate", "UploadRate":
			value = upload
		case "download_rate", "DownloadRate":
			value = download
		}
		values[i] = entry{row, value}
	}
	sort.SliceStable(values, func(i, j int) bool {
		if order == "asc" {
			return values[i].value < values[j].value
		}
		return values[i].value > values[j].value
	})
	for i, entry := range values {
		rows[i] = entry.row
	}
}
