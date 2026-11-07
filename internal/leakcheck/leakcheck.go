// Package leakcheck fails a test binary whose tests leave goroutines blocked
// for good, on channels, locks or other primitives nothing can reach any
// more, as the goroutineleak profile of runtime/pprof finds them. It does not
// see goroutines blocked on I/O, which may still end.
package leakcheck

import (
	"bytes"
	"fmt"
	"os"
	"runtime/pprof"
	"testing"
)

// Main runs the tests of m and then checks for leaked goroutines; use it as
//
//	func TestMain(m *testing.M) { leakcheck.Main(m) }
func Main(m *testing.M) {
	code := m.Run()
	if code == 0 {
		if err := Check(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}
	os.Exit(code)
}

// Check runs a garbage collection that detects leaked goroutines and
// returns an error with their stacks if it found any.
func Check() error {
	p := pprof.Lookup("goroutineleak")
	var b bytes.Buffer
	if err := p.WriteTo(&b, 1); err != nil {
		return fmt.Errorf("leakcheck: %w", err)
	}
	if n := p.Count(); n > 0 {
		return fmt.Errorf("leakcheck: %d goroutines leaked:\n%s", n, b.String())
	}
	return nil
}
