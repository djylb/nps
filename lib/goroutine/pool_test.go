package goroutine

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
)

func TestCopyBuffer_HTTPDetectionAndCopy(t *testing.T) {
	input := "GET /hello HTTP/1.1\r\nHost: example.com\r\n\r\n"
	src := bytes.NewBufferString(input)
	var dst bytes.Buffer

	task := &file.Tunnel{Target: &file.Target{TargetStr: "127.0.0.1:80"}}
	written, err := CopyBuffer(&dst, src, nil, task, "127.0.0.1:12345")
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
	if written != int64(len(input)) {
		t.Fatalf("unexpected written bytes, got=%d want=%d", written, len(input))
	}
	if dst.String() != input {
		t.Fatalf("unexpected copied content, got=%q want=%q", dst.String(), input)
	}
	if !task.IsHttp {
		t.Fatalf("expected task.IsHttp=true for HTTP request")
	}
}

func TestCopyBuffer_FlowLimitExceeded(t *testing.T) {
	src := bytes.NewBufferString("abcd")
	var dst bytes.Buffer

	flow := &file.Flow{FlowLimit: 1, ExportFlow: (1 << 20) - 2}
	written, err := CopyBuffer(&dst, src, []*file.Flow{flow}, nil, "")
	if err == nil || !strings.Contains(err.Error(), "flow limit exceeded") {
		t.Fatalf("expected flow limit exceeded error, got %v", err)
	}
	if written != 4 {
		t.Fatalf("unexpected written bytes, got=%d want=4", written)
	}
}

func TestCopyBuffer_TimeLimitExceeded(t *testing.T) {
	src := bytes.NewBufferString("abc")
	var dst bytes.Buffer

	flow := &file.Flow{TimeLimit: time.Now().Add(-time.Second)}
	written, err := CopyBuffer(&dst, src, []*file.Flow{flow}, nil, "")
	if err == nil || !strings.Contains(err.Error(), "time limit exceeded") {
		t.Fatalf("expected time limit exceeded error, got %v", err)
	}
	if written != 3 {
		t.Fatalf("unexpected written bytes, got=%d want=3", written)
	}
}
