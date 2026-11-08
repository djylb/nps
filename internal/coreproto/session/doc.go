// Package session runs the core protocol on zmux: Admission on the shared
// bridge ports, Establish on both sides, the client table and the sessions.
// It implements coreapi; nothing above it sees zmux or wire types.
package session
