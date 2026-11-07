package file

import (
	"reflect"
	"sync"
	"testing"

	"github.com/djylb/nps/lib/model"
)

func TestSortClientByKey(t *testing.T) {
	clients := &sync.Map{}
	clients.Store("a", &model.Client{Id: 1, Flow: &model.Flow{ExportFlow: 10, InletFlow: 100}})
	clients.Store("b", &model.Client{Id: 2, Flow: &model.Flow{ExportFlow: 30, InletFlow: 50}})
	clients.Store("c", &model.Client{Id: 3, Flow: &model.Flow{ExportFlow: 20, InletFlow: 80}})
	clients.Store("d", &model.Client{Id: 4}) // no flow: zero traffic
	clients.Store("e", "not a client")

	if got := sortClientByKey(clients, "ExportFlow", "desc"); !reflect.DeepEqual(got, []int{2, 3, 1, 4}) {
		t.Fatalf("ExportFlow desc = %v, want [2 3 1 4]", got)
	}
	if got := sortClientByKey(clients, "InletFlow", "asc"); !reflect.DeepEqual(got, []int{4, 2, 3, 1}) {
		t.Fatalf("InletFlow asc = %v, want [4 2 3 1]", got)
	}
}
