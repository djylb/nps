package pool

import (
	"math/rand"
	"sync"
	"testing"
	"time"
)

func drain(n int) []int {
	pl := New[int]()
	for i := 0; i < n; i++ {
		pl.Push(i)
	}
	var out []int
	for {
		v, ok := pl.Dequeue()
		if !ok {
			break
		}
		out = append(out, v)
	}
	return out
}

func TestFIFOSequence(t *testing.T) {
	for n := 1; n <= 19; n++ {
		want := make([]int, n)
		for i := range want {
			want[i] = i
		}
		got := drain(n)
		if len(got) != n {
			t.Fatalf("n=%d: expected length %d, got %d", n, n, len(got))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("n=%d: FIFO violated at index %d: want %v, got %v (full=%v)",
					n, i, want, got, got)
			}
		}
	}
}

func TestConcurrentMixedOps(t *testing.T) {
	const (
		workers    = 8
		iterations = 10_000
	)
	pl := New[int]()

	var wg sync.WaitGroup
	wg.Add(workers)

	for w := 0; w < workers; w++ {
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))
			for i := 0; i < iterations; i++ {
				op := r.Intn(4)
				switch op {
				case 0: // Push
					pl.Push(r.Intn(1_000_000))
				case 1: // Pop
					pl.Pop()
				case 2: // Remove
					if v, ok := pl.Peek(); ok {
						pl.Remove(v)
					}
				case 3: // Dequeue
					pl.Dequeue()
				}
			}
		}(w)
	}
	wg.Wait()

	seen := make(map[int]struct{})
	for {
		v, ok := pl.Dequeue()
		if !ok {
			break
		}
		if _, dup := seen[v]; dup {
			t.Fatalf("duplicate value %d found after concurrent ops", v)
		}
		seen[v] = struct{}{}
	}
}

func TestRandomEnqueueDequeueOrderRatio(t *testing.T) {
	rand.Seed(time.Now().UnixNano())

	const (
		cycles      = 100
		maxPush     = 10000
		repetitions = 3
	)

	for run := 1; run <= repetitions; run++ {
		pl := New[int]()
		nextVal := 0
		var seq []int

		for step := 0; step < cycles; step++ {
			pushN := rand.Intn(maxPush + 1)
			//if step <= 1 {
			//	pushN = maxPush
			//}
			for i := 0; i < pushN; i++ {
				pl.Push(nextVal)
				nextVal++
			}

			popN := 0
			if sz := pl.Size(); sz > 0 {
				//popN = rand.Intn(sz + 1)
				popN = rand.Intn(maxPush + 1)
				if popN > sz+1 {
					popN = sz + 1
				}
			}
			for i := 0; i < popN; i++ {
				if v, ok := pl.Dequeue(); ok {
					seq = append(seq, v)
				} else {
					break
				}
			}
		}

		for {
			v, ok := pl.Dequeue()
			if !ok {
				break
			}
			seq = append(seq, v)
		}

		if len(seq) < 2 {
			t.Logf("run %d: no enough data", run)
			continue
		}
		pivot := seq[0]
		ordered := 0
		for i := 1; i < len(seq); i++ {
			if seq[i] > pivot {
				ordered++
				pivot = seq[i]
			} else if seq[i] > seq[i-1] {
				ordered++
			}
		}
		ratio := float64(ordered) / float64(len(seq)-1)
		t.Logf("run %2d: ordered-ratio = %.4f  (up-count %d / pairs %d)",
			run, ratio*100, ordered, len(seq)-1)
	}
}

func TestOrderRatioByMaxPush(t *testing.T) {
	rand.Seed(time.Now().UnixNano())

	const (
		cycles      = 1000
		repetitions = 10
	)
	l := 1
	for maxPush := 0; maxPush <= 100; maxPush += l {
		if maxPush == 10 {
			l = 10
		}
		var ratios []float64
		for run := 1; run <= repetitions; run++ {
			pl := New[int]()
			nextVal := 0
			var seq []int

			for step := 0; step < cycles; step++ {
				pushN := rand.Intn(maxPush + 1)
				for i := 0; i < pushN; i++ {
					pl.Push(nextVal)
					nextVal++
				}

				popN := 0
				if sz := pl.Size(); sz > 0 {
					popN = rand.Intn(sz + 1)
					//popN = rand.Intn(maxPush + 1)
					if popN > maxPush+1 {
						popN = rand.Intn(maxPush + 1)
					}
				}
				for i := 0; i < popN; i++ {
					if v, ok := pl.Dequeue(); ok {
						seq = append(seq, v)
					} else {
						break
					}
				}
			}

			for {
				v, ok := pl.Dequeue()
				if !ok {
					break
				}
				seq = append(seq, v)
			}

			if len(seq) < 2 {
				ratios = append(ratios, 1.0)
				continue
			}

			pivot := seq[0]
			ordered := 0
			for i := 1; i < len(seq); i++ {
				if seq[i] > pivot {
					ordered++
					pivot = seq[i]
				} else if seq[i] > seq[i-1] {
					ordered++
				}
			}
			ratios = append(ratios, float64(ordered)/float64(len(seq)-1))
		}

		sum := 0.0
		for _, r := range ratios {
			sum += r
		}
		avg := sum / float64(len(ratios))

		//t.Logf("maxPush=%3d  ratios=%v  avg=%.4f", maxPush, ratios, avg)
		t.Logf("maxPush=%3d avg=%.4f", maxPush, avg*100)
	}
}

func TestStackOrderRatioByMaxPush(t *testing.T) {
	rand.Seed(time.Now().UnixNano())

	const (
		cycles      = 1000
		repetitions = 10
	)

	for maxPush := 0; maxPush <= 30; maxPush += 10 {
		var ratios []float64
		for run := 1; run <= repetitions; run++ {
			pl := New[int]()
			nextVal := 0
			var seq []int

			for step := 0; step < cycles; step++ {
				pushN := rand.Intn(maxPush + 1)
				for i := 0; i < pushN; i++ {
					pl.Push(nextVal)
					nextVal++
				}

				popN := 0
				if sz := pl.Size(); sz > 0 {
					//popN = rand.Intn(sz + 1)
					popN = rand.Intn(maxPush + 1)
					if popN > sz+1 {
						popN = sz + 1
					}
				}
				for i := 0; i < popN; i++ {
					if v, ok := pl.Pop(); ok {
						seq = append(seq, v)
					} else {
						break
					}
				}
			}

			for {
				v, ok := pl.Pop()
				if !ok {
					break
				}
				seq = append(seq, v)
			}

			if len(seq) < 2 {
				ratios = append(ratios, 1.0)
				continue
			}

			pivot := seq[0]
			ordered := 0
			for i := 1; i < len(seq); i++ {
				if seq[i] < pivot {
					ordered++
					pivot = seq[i]
				} else if seq[i] < seq[i-1] {
					ordered++
				}
			}
			ratios = append(ratios, float64(ordered)/float64(len(seq)-1))
		}

		sum := 0.0
		for _, r := range ratios {
			sum += r
		}
		avg := sum / float64(len(ratios))

		//t.Logf("maxPush=%3d  ratios=%v  avg=%.4f", maxPush, ratios, avg)
		t.Logf("maxPush=%3d avg=%.4f", maxPush, avg*100)
	}
}
