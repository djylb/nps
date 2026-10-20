package service

import (
	"testing"

	"github.com/djylb/nps/lib/file"
)

type globalConnectionRepository struct {
	stubRepository
	saved *file.Glob
}

func (r *globalConnectionRepository) SaveGlobal(g *file.Glob) error { r.saved = g; return nil }

func TestGlobalConnectionInput(t *testing.T) {
	repo := new(globalConnectionRepository)
	s := DefaultGlobalService{Backend: Backend{Repository: repo}}
	if err := s.Save(SaveGlobalInput{MaxConn: 7}); err != nil {
		t.Fatal(err)
	}
	if repo.saved.MaxConn != 7 {
		t.Fatal("global limit not saved")
	}
	if err := s.Save(SaveGlobalInput{MaxConn: -1}); err != nil {
		t.Fatal(err)
	}
	if repo.saved.MaxConn != 0 {
		t.Fatal("negative global limit not normalized")
	}
}
