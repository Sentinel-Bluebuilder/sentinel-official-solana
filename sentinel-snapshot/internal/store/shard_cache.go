package store

import (
	"sync"
)

type shardCache struct {
	shards [256]struct {
		mu sync.Mutex
		m  map[string][]byte
	}
}

func newShardCache() *shardCache {
	c := &shardCache{}
	for i := range c.shards {
		c.shards[i].m = make(map[string][]byte)
	}

	return c
}

func (c *shardCache) get(hash []byte) ([]byte, bool) {
	if len(hash) == 0 {
		return nil, false
	}

	s := &c.shards[hash[0]]
	s.mu.Lock()
	v, ok := s.m[string(hash)]
	s.mu.Unlock()

	return v, ok
}

func (c *shardCache) put(hash, val []byte) {
	if len(hash) == 0 {
		return
	}

	s := &c.shards[hash[0]]
	s.mu.Lock()
	s.m[string(hash)] = val
	s.mu.Unlock()
}
