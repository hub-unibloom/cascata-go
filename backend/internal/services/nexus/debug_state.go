package nexus

import (
	"encoding/json"
	"sync"
	"time"
)

type DebugState struct {
	Active    bool
	ExpiresAt time.Time
	StateJSON []byte
}

var (
	debugMu    sync.RWMutex
	debugStore = make(map[string]*DebugState)
)

func SetDebugMode(key string, active bool) {
	debugMu.Lock()
	defer debugMu.Unlock()
	if active {
		debugStore[key] = &DebugState{
			Active:    true,
			ExpiresAt: time.Now().Add(5 * time.Minute),
		}
	} else {
		delete(debugStore, key)
	}
}

func IsDebugActive(key string) bool {
	debugMu.RLock()
	defer debugMu.RUnlock()
	state, ok := debugStore[key]
	if ok && time.Now().Before(state.ExpiresAt) {
		return true
	}
	return false
}

func PublishDebugState(key string, state interface{}) {
	debugMu.Lock()
	defer debugMu.Unlock()
	
	s, ok := debugStore[key]
	if !ok || time.Now().After(s.ExpiresAt) {
		return
	}
	
	data, err := json.Marshal(state)
	if err == nil {
		s.StateJSON = data
	}
}

func GetDebugState(key string) []byte {
	debugMu.RLock()
	defer debugMu.RUnlock()
	state, ok := debugStore[key]
	if ok && time.Now().Before(state.ExpiresAt) {
		return state.StateJSON
	}
	return nil
}

func ConsumeDebugFlag(key string) {
	debugMu.Lock()
	defer debugMu.Unlock()
	// Disable it so it only runs once per click
	delete(debugStore, key)
}
