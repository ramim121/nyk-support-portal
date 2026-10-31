package main

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// tokenStore holds short-lived bearer tokens handed to technicians after a
// successful password login. Tokens live in memory only; a server restart logs
// everyone out, which is fine for a small support team.
type tokenStore struct {
	mu     sync.Mutex
	tokens map[string]tokenEntry
	ttl    time.Duration
}

type tokenEntry struct {
	username string
	expires  time.Time
}

func newTokenStore(ttl time.Duration) *tokenStore {
	ts := &tokenStore{tokens: make(map[string]tokenEntry), ttl: ttl}
	go ts.gc()
	return ts
}

// issue creates a new random token for username.
func (ts *tokenStore) issue(username string) string {
	tok := randomToken(32)
	ts.mu.Lock()
	ts.tokens[tok] = tokenEntry{username: username, expires: time.Now().Add(ts.ttl)}
	ts.mu.Unlock()
	return tok
}

// lookup returns the username for a valid, unexpired token.
func (ts *tokenStore) lookup(tok string) (string, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	e, ok := ts.tokens[tok]
	if !ok || time.Now().After(e.expires) {
		if ok {
			delete(ts.tokens, tok)
		}
		return "", false
	}
	return e.username, true
}

// revoke drops a token (logout).
func (ts *tokenStore) revoke(tok string) {
	ts.mu.Lock()
	delete(ts.tokens, tok)
	ts.mu.Unlock()
}

func (ts *tokenStore) gc() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		ts.mu.Lock()
		for k, e := range ts.tokens {
			if now.After(e.expires) {
				delete(ts.tokens, k)
			}
		}
		ts.mu.Unlock()
	}
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
