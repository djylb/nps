package bridge

import (
	"testing"

	"github.com/djylb/lifecycle/lifecycletest"
)

func TestMain(m *testing.M) { // kcp-go starts its post-process workers once for the process
	lifecycletest.VerifyMain(m, "kcp-go/v5.postProcessWorker")
}
