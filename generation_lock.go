package ads

import (
	"context"
	"sync"
)

// generationMutex is a writer-preferring RW lock with cancellable read admission.
// Lifecycle writers remain synchronous; operation callers can abandon their wait.
type generationMutex struct {
	mu                      sync.Mutex
	changed                 chan struct{}
	readers, waitingWriters int
	writer                  bool
}

func (m *generationMutex) changedLocked() <-chan struct{} {
	if m.changed == nil {
		m.changed = make(chan struct{})
	}
	return m.changed
}
func (m *generationMutex) notifyLocked() {
	if m.changed != nil {
		close(m.changed)
		m.changed = nil
	}
}
func (m *generationMutex) RLockContext(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !m.writer && m.waitingWriters == 0 {
			m.readers++
			return nil
		}
		changed := m.changedLocked()
		m.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
		}
		m.mu.Lock()
	}
}
func (m *generationMutex) RLock() { _ = m.RLockContext(context.Background()) }
func (m *generationMutex) RUnlock() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readers--
	if m.readers == 0 {
		m.notifyLocked()
	}
}
func (m *generationMutex) Lock() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.waitingWriters++
	for m.writer || m.readers != 0 {
		ch := m.changedLocked()
		m.mu.Unlock()
		<-ch
		m.mu.Lock()
	}
	m.waitingWriters--
	m.writer = true
}
func (m *generationMutex) Unlock() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writer = false
	m.notifyLocked()
}
