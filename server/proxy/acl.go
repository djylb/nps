package proxy

import (
	"net"
	"strconv"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/ipreg"
	"github.com/djylb/nps/lib/logs"
)

// sourcePolicies returns the source access lists of the global settings,
// client and entry, a tunnel or host; missing levels allow everything.
func sourcePolicies(client *file.Client, entry *file.SourceLists) (global, cli, ent *acl.Source) {
	if g := file.GetDb().GetGlobal(); g != nil {
		global = g.SourceACL()
	}
	if client != nil {
		cli = client.SourceACL()
	}
	if entry != nil {
		ent = entry.SourceACL()
	}
	return
}

// SourceDenied reports whether the access lists of the global settings, the
// client and the entry deny the visitor addr, and logs a denial. entryName
// names the entry in the log, for example "tunnel 3".
func SourceDenied(addr net.Addr, client *file.Client, entry *file.SourceLists, entryName string) bool {
	g, c, e := sourcePolicies(client, entry)
	if !acl.Allows(addr, g, c, e) {
		logs.Warn("Access from %v %v", addr, acl.Decide(addr, levelNames(client, entryName), g, c, e))
		return true
	}
	if client != nil && registrationRequired(client, entry) && !ipreg.RegisteredAddr(client.Id, addr) {
		logs.Warn("Access from %v denied: the address is not registered for client %d (%s)", addr, client.Id, entryName)
		return true
	}
	return false
}

// SourceDeniedString is SourceDenied for an address given as text.
func SourceDeniedString(addr string, client *file.Client, entry *file.SourceLists, entryName string) bool {
	g, c, e := sourcePolicies(client, entry)
	if !acl.AllowsString(addr, g, c, e) {
		logs.Warn("Access from %s %v", addr, acl.DecideString(addr, levelNames(client, entryName), g, c, e))
		return true
	}
	if client != nil && registrationRequired(client, entry) && !ipreg.RegisteredString(client.Id, addr) {
		logs.Warn("Access from %s denied: the address is not registered for client %d (%s)", addr, client.Id, entryName)
		return true
	}
	return false
}

// datagramDenied is SourceDenied for single datagrams, which are dropped
// with a trace entry only so that a flood of them does not flood the log.
func datagramDenied(addr net.Addr, client *file.Client, entry *file.SourceLists) bool {
	g, c, e := sourcePolicies(client, entry)
	if acl.Allows(addr, g, c, e) && (client == nil || !registrationRequired(client, entry) || ipreg.RegisteredAddr(client.Id, addr)) {
		return false
	}
	logs.Trace("Datagram from %v dropped by the access lists", addr)
	return true
}

// registrationRequired reports whether the client or the entry only allows
// visitor addresses registered for the client.
func registrationRequired(client *file.Client, entry *file.SourceLists) bool {
	if client == nil {
		return false
	}
	return client.RegisteredOnly || (entry != nil && entry.RegisteredOnly)
}

// levelNames names the levels of a decision for the log.
func levelNames(client *file.Client, entryName string) []string {
	clientName := "client"
	if client != nil {
		clientName = "client " + strconv.Itoa(client.Id)
	}
	return []string{"global", clientName, entryName}
}

func taskName(t *file.Tunnel) string {
	if t == nil {
		return "tunnel"
	}
	if t.Id == 0 {
		// built for a host entry by NewTunnelByHost
		return "host"
	}
	return "tunnel " + strconv.Itoa(t.Id)
}

func taskLists(t *file.Tunnel) *file.SourceLists {
	if t == nil {
		return nil
	}
	return &t.SourceLists
}
