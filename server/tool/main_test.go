package tool

import (
	"testing"

	"github.com/djylb/nps/internal/leakcheck"
)

func TestMain(m *testing.M) { leakcheck.Main(m) }
