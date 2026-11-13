package bridge

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/djylb/nps/lib/model"
)

// ClientLink summarizes how the core sessions of client id are carried now:
// the carriers of their pipes and their record modes. It reads the sessions,
// so it follows pipes joining and leaving. nil when the client has no core
// session.
func (s *Bridge) ClientLink(id int) *model.ClientLink {
	v, ok := s.Client.Load(id)
	if !ok {
		return nil
	}
	var pipes, records []string
	found := false
	for _, node := range v.(*Client).nodeList() {
		if cs := node.coreSession(); cs != nil {
			found = true
			pipes = append(pipes, cs.Pipes()...)
			if r := cs.Record(); !slices.Contains(records, r) {
				records = append(records, r)
			}
		}
	}
	if !found {
		return nil
	}
	slices.Sort(records)
	return &model.ClientLink{Pipes: pipeSummary(pipes), Records: records}
}

// pipeSummary counts carriers: "tls×2 + quic", the most used first, ties by
// name.
func pipeSummary(carriers []string) string {
	count := map[string]int{}
	for _, c := range carriers {
		count[c]++
	}
	names := make([]string, 0, len(count))
	for n := range count {
		names = append(names, n)
	}
	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(cmp.Compare(count[b], count[a]), cmp.Compare(a, b))
	})
	parts := make([]string, len(names))
	for i, n := range names {
		if parts[i] = n; count[n] > 1 {
			parts[i] = fmt.Sprintf("%s×%d", n, count[n])
		}
	}
	return strings.Join(parts, " + ")
}
