package conn

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// readInfoPayload feeds payload as a length-prefixed info message and returns
// the result of read.
func readInfoPayload(t *testing.T, payload string, read func(*Conn) error) error {
	t.Helper()
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	_ = server.SetDeadline(time.Now().Add(5 * time.Second))
	go func() {
		_ = binary.Write(client, binary.LittleEndian, int32(len(payload)))
		_, _ = client.Write([]byte(payload))
	}()
	return read(NewConn(server))
}

func TestGetConfigInfoRejectsEmptyPayload(t *testing.T) {
	for _, payload := range []string{"null", `{"Id":1,`, "x"} {
		err := readInfoPayload(t, payload, func(c *Conn) error {
			cfg, err := c.GetConfigInfo()
			if cfg != nil {
				t.Fatalf("payload %q returned config %+v", payload, cfg)
			}
			return err
		})
		if err == nil {
			t.Fatalf("payload %q: expected error", payload)
		}
	}
	for _, read := range []func(*Conn) error{
		func(c *Conn) error { _, err := c.GetHostInfo(); return err },
		func(c *Conn) error { _, err := c.GetTaskInfo(); return err },
	} {
		if err := readInfoPayload(t, "null", read); err == nil {
			t.Fatal("null payload: expected error")
		}
	}
}

func TestGetConfigInfoKeepsLenientFieldTypes(t *testing.T) {
	var got string
	err := readInfoPayload(t, `{"Id":"1","Remark":"demo"}`, func(c *Conn) error {
		cfg, err := c.GetConfigInfo()
		if cfg != nil {
			got = cfg.Remark
		}
		return err
	})
	if err != nil || got != "demo" {
		t.Fatalf("GetConfigInfo() = %q, %v", got, err)
	}
}
