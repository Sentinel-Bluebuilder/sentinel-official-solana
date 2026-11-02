package service

import (
	"fmt"
	"sync"
)

type Peers[V any] struct {
	items map[PeerID]V
	mu    sync.RWMutex
}

func NewPeers[V any]() *Peers[V] {
	return &Peers[V]{items: make(map[PeerID]V)}
}

func (p *Peers[V]) Add(id PeerID, value V) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.items[id]; ok {
		return fmt.Errorf("%w: %s", ErrPeerExists, id)
	}

	p.items[id] = value

	return nil
}

func (p *Peers[V]) Get(id PeerID) (V, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	value, ok := p.items[id]
	if !ok {
		var zero V

		return zero, fmt.Errorf("%w: %s", ErrPeerNotFound, id)
	}

	return value, nil
}

func (p *Peers[V]) Has(id PeerID) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	_, ok := p.items[id]

	return ok
}

func (p *Peers[V]) Remove(id PeerID) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.items[id]; !ok {
		return fmt.Errorf("%w: %s", ErrPeerNotFound, id)
	}

	delete(p.items, id)

	return nil
}

func (p *Peers[V]) Set(id PeerID, value V) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.items[id]; !ok {
		return fmt.Errorf("%w: %s", ErrPeerNotFound, id)
	}

	p.items[id] = value

	return nil
}
