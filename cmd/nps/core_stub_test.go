package main

import "github.com/djylb/nps/internal/coreproto/coreapi"

// coreStub is the entry of tests that run no bridge ports.
type coreStub struct{ coreapi.Entry }

func (coreStub) Kick(int, bool)                                       {}
func (coreStub) RevokeLinks(func(tunnel, visitor, provider int) bool) {}
