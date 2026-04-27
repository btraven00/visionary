package session

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type Session struct {
	ID           string
	TokenLabel   string
	ImageBytes   []byte
	ImageMIME    string
	PromptBase   string
	LastResponse string
	CreatedAt    time.Time
	LastUsedAt   time.Time
	Mu           sync.Mutex
}

type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	ttl      time.Duration
	idle     time.Duration
}

func NewStore(ttl, idle time.Duration) *Store {
	s := &Store{
		sessions: make(map[string]*Session),
		ttl:      ttl,
		idle:     idle,
	}
	go s.sweep()
	return s
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) Create(label string, imageBytes []byte, imageMIME, promptBase string) *Session {
	sess := &Session{
		ID:         newID(),
		TokenLabel: label,
		ImageBytes: imageBytes,
		ImageMIME:  imageMIME,
		PromptBase: promptBase,
		CreatedAt:  time.Now(),
		LastUsedAt: time.Now(),
	}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	return sess
}

// Get returns the session only if it exists and is owned by tokenLabel.
// Returns nil if not found or unauthorized (caller should return 404 to avoid leaking existence).
func (s *Store) Get(id, tokenLabel string) *Session {
	s.mu.RLock()
	sess, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok || sess.TokenLabel != tokenLabel {
		return nil
	}
	return sess
}

func (s *Store) Delete(id, tokenLabel string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok || sess.TokenLabel != tokenLabel {
		return false
	}
	delete(s.sessions, id)
	return true
}

func (s *Store) sweep() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for id, sess := range s.sessions {
			if now.Sub(sess.LastUsedAt) > s.idle || now.Sub(sess.CreatedAt) > s.ttl {
				delete(s.sessions, id)
			}
		}
		s.mu.Unlock()
	}
}
