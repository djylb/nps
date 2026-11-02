package rate

import (
	"sync"
	"time"
	"weak"

	"github.com/djylb/byterate"
)

// The Counters whose throughput is shown, sampled once a second so that
// their speeds do not depend on how often they are read. Counters are held
// weakly and dropped once their owners are gone.
var (
	sampledMu sync.Mutex
	sampled   []weak.Pointer[byterate.Counter]
	sampler   sync.Once
)

// sample samples c's throughput every second from now on.
func sample(c *byterate.Counter) {
	c.Sample()
	sampledMu.Lock()
	sampled = append(sampled, weak.Make(c))
	sampledMu.Unlock()
	sampler.Do(func() { go sampleLoop() })
}

func sampleLoop() {
	for range time.Tick(time.Second) {
		sampledMu.Lock()
		live := sampled[:0]
		for _, p := range sampled {
			if c := p.Value(); c != nil {
				c.Sample()
				live = append(live, p)
			}
		}
		clear(sampled[len(live):])
		sampled = live
		sampledMu.Unlock()
	}
}
