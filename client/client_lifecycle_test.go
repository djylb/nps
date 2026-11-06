package client

import "testing"

func TestTRPClientCloseWithoutStartDoesNotPanic(t *testing.T) {
	c := &TRPClient{}
	c.Close()
	c.Close()
}
