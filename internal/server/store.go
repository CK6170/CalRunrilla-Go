package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/CK6170/Calrunrilla-go/models"
)

type configKind string

const (
	kindConfig     configKind = "config"
	kindCalibrated configKind = "calibrated"
)

type ConfigRecord struct {
	ID   string
	Kind configKind
	Raw  []byte
	P    *models.PARAMETERS
}

type ConfigStore struct {
	mu sync.RWMutex
	m  map[string]*ConfigRecord
}

func NewConfigStore() *ConfigStore {
	return &ConfigStore{m: make(map[string]*ConfigRecord)}
}

func (s *ConfigStore) Put(kind configKind, raw []byte, p *models.PARAMETERS) (*ConfigRecord, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	rec := &ConfigRecord{ID: id, Kind: kind, Raw: raw, P: p}
	s.mu.Lock()
	s.m[id] = rec
	s.mu.Unlock()
	return rec, nil
}

func (s *ConfigStore) Get(id string) (*ConfigRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.m[id]
	return r, ok
}

func newID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

