package file

import (
	"context"
	"net"
	"os"
	"time"

	"github.com/djylb/geomatch"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/slogx"
)

// GlobalDenied reports whether the global access lists, the firewall in
// front of every listener of nps, deny addr, and logs a denial. what names
// the listener in the log.
func (s *DbUtils) GlobalDenied(addr net.Addr, what string) bool {
	g := s.GetGlobal()
	if g == nil {
		return false
	}
	src := g.SourceACL()
	if src == nil || acl.Allows(addr, src, nil, nil) {
		return false
	}
	slogx.Emit(context.Background(), log, &logevent.TunnelDenied, "src_ip", netaddr.Host(addr.String()), "route", what, "reason", "acl", "rule", acl.Decide(addr, []string{"global"}, src, nil, nil))
	return true
}

// GlobalDeniedString is GlobalDenied for an address given as text.
func (s *DbUtils) GlobalDeniedString(addr, what string) bool {
	g := s.GetGlobal()
	if g == nil {
		return false
	}
	src := g.SourceACL()
	if src == nil || acl.AllowsString(addr, src, nil, nil) {
		return false
	}
	slogx.Emit(context.Background(), log, &logevent.TunnelDenied, "src_ip", netaddr.Host(addr), "route", what, "reason", "acl", "rule", acl.DecideString(addr, []string{"global"}, src, nil, nil))
	return true
}

// RefreshAccessLists recompiles the access lists whose geodata changed, and
// logs the lists that keep their rules because the new geodata breaks some.
func RefreshAccessLists() {
	reportGeoRefresh(acl.RefreshGeoData())
}

// WatchAccessLists checks the geodata files every interval until ctx ends
// and refreshes the access lists when one of them changed, so that updated
// geodata takes effect. Checking reads file metadata only.
func WatchAccessLists(ctx context.Context, interval time.Duration) {
	_ = acl.WatchGeoData(ctx, interval, reportGeoRefresh)
}

// ReportGeoData logs the geodata files the access lists use, as
// source_loaded for each file present.
func ReportGeoData() {
	ipFile, siteFile := acl.GeoFiles()
	for _, f := range []struct{ name, file string }{{"geoip", ipFile}, {"geosite", siteFile}} {
		if f.file == "" {
			continue
		}
		if _, err := os.Stat(f.file); err != nil {
			log.Debug("geodata file not present", "name", f.name, "file", f.file)
			continue
		}
		slogx.Emit(context.Background(), log, &logevent.SourceLoaded, "name", f.name, "file", f.file)
	}
}

func reportGeoRefresh(res geomatch.RefreshResult) {
	for _, l := range res.Failed {
		owner, _ := l.Tag().(string)
		if owner == "" {
			owner = "an access list"
		}
		log.Warn("geodata update not applied to some rules", "owner", owner, "err", acl.RefreshError(l))
	}
}
