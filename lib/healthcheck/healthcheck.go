// Package healthcheck holds the messages of server-given health checks, apart
// from any protocol that carries them.
package healthcheck

// Group is a set of targets a client checks with the same settings, on behalf
// of one tunnel or host.
type Group struct {
	Name     string
	Type     string // tcp, http or https
	Path     string // of an http or https check
	Interval int    // seconds between checks
	Timeout  int    // seconds a check may take
	Fall     int    // failures in a row that take a target down
	Targets  []string
}

// Report says that a target of a group is up or not.
type Report struct {
	Group string // empty for a check an old client configured itself
	Addr  string
	Up    bool
}
