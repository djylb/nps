package model

import (
	"slices"
	"sync/atomic"

	"github.com/djylb/nps/lib/acl"
)

// SourceLists are the source access lists of a level, the global settings, a
// client, a tunnel or a host: which visitor addresses may connect. Every
// level has to allow an address.
type SourceLists struct {
	// IpListPriority decides an address on both lists or on neither when
	// both are set: acl.DenyFirst (0) or acl.AllowFirst (1).
	IpListPriority int
	BlackIpList    []string
	WhiteIpList    []string
	sourceAcl      atomic.Pointer[acl.Source]
	// RegisteredOnly additionally requires the visitor address to be
	// registered for the client, see package ipreg. The global settings do
	// not use it.
	RegisteredOnly bool
	compiled       atomic.Bool
}

// SetSourceLists replaces the lists and compiles them.
func (l *SourceLists) SetSourceLists(priority int, black, white []string, scope acl.Scope, owner string) {
	l.IpListPriority = acl.NormalizePriority(priority)
	l.BlackIpList = slices.Clone(black)
	l.WhiteIpList = slices.Clone(white)
	l.CompileSourceLists(scope, owner)
}

// CopySourceLists copies the lists of other and their compiled form.
func (l *SourceLists) CopySourceLists(other *SourceLists) {
	l.RegisteredOnly = other.RegisteredOnly
	l.IpListPriority = other.IpListPriority
	l.BlackIpList = slices.Clone(other.BlackIpList)
	l.WhiteIpList = slices.Clone(other.WhiteIpList)
	l.sourceAcl.Store(other.sourceAcl.Load())
	l.compiled.Store(other.compiled.Load())
}

// CompileSourceLists compiles the lists. Rules that cannot be used, such as
// geoip: rules without geodata, are logged and skipped.
func (l *SourceLists) CompileSourceLists(scope acl.Scope, owner string) {
	src, err := acl.CompileSource(scope, l.IpListPriority, l.BlackIpList, l.WhiteIpList, "the access lists of "+owner)
	if err != nil {
		log.Warn("access list rules skipped", "owner", owner, "err", err)
	}
	l.sourceAcl.Store(src)
	l.compiled.Store(true)
}

// SourceACL returns the compiled lists, compiling them as user lists if that
// has not happened yet.
func (l *SourceLists) SourceACL() *acl.Source {
	if l == nil {
		return nil
	}
	if !l.compiled.Load() {
		l.CompileSourceLists(acl.ScopeUser, "an entry")
	}
	return l.sourceAcl.Load()
}
