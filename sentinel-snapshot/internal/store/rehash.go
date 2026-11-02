package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync/atomic"
)

const (
	nodeKeyPrefix = 'n'
	checkInterval = 4096
	maxTreeDepth  = 128
)

var ErrMissingNode = errors.New("iavl node missing from store")

type writer struct{ b []byte }

func (w *writer) writeBytes(b []byte) {
	var tmp [binary.MaxVarintLen64]byte

	n := binary.PutUvarint(tmp[:], uint64(len(b)))
	w.b = append(w.b, tmp[:n]...)
	w.b = append(w.b, b...)
}

func (w *writer) writeVarint(i int64) {
	var tmp [binary.MaxVarintLen64]byte

	n := binary.PutVarint(tmp[:], i)
	w.b = append(w.b, tmp[:n]...)
}

type node struct {
	height    int64
	size      int64
	version   int64
	key       []byte
	isLeaf    bool
	value     []byte
	leftHash  []byte
	rightHash []byte
}

func hashInner(n *node, leftHash, rightHash []byte) []byte {
	var w writer
	w.writeVarint(n.height)
	w.writeVarint(n.size)
	w.writeVarint(n.version)
	w.writeBytes(leftHash)
	w.writeBytes(rightHash)
	sum := sha256.Sum256(w.b)

	return sum[:]
}

func hashLeaf(n *node) []byte {
	var w writer
	w.writeVarint(0)
	w.writeVarint(n.size)
	w.writeVarint(n.version)
	w.writeBytes(n.key)
	vh := sha256.Sum256(n.value)
	w.writeBytes(vh[:])
	sum := sha256.Sum256(w.b)

	return sum[:]
}

func parseNode(raw []byte) (*node, error) {
	r := &reader{b: raw}

	h, err := r.readVarint()
	if err != nil {
		return nil, err
	}

	size, err := r.readVarint()
	if err != nil {
		return nil, err
	}

	ver, err := r.readVarint()
	if err != nil {
		return nil, err
	}

	key, err := r.readBytes()
	if err != nil {
		return nil, err
	}

	n := &node{
		height:  h,
		size:    size,
		version: ver,
		key:     key,
		isLeaf:  h == 0,
	}
	if n.isLeaf {
		v, err := r.readBytes()
		if err != nil {
			return nil, err
		}

		n.value = v
	} else {
		l, err := r.readBytes()
		if err != nil {
			return nil, err
		}

		rr, err := r.readBytes()
		if err != nil {
			return nil, err
		}

		n.leftHash, n.rightHash = l, rr
	}

	if r.pos != len(raw) {
		return nil, fmt.Errorf("trailing bytes in node record (%d of %d consumed)", r.pos, len(raw))
	}

	return n, nil
}

type nodeGetter interface {
	Get(key []byte) ([]byte, error)
}

func RehashTree(ctx context.Context, db nodeGetter, rootHash []byte) ([]byte, error) {
	if len(rootHash) == 0 {
		return emptyRoot(), nil
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("rehash tree: %w", err)
	}

	walk := &sequentialWalk{cache: make(map[string][]byte)}

	emptyHash := emptyTreeHash()
	if bytes.Equal(rootHash, emptyHash[:]) {
		raw, err := db.Get(append([]byte{nodeKeyPrefix}, rootHash...))
		if err != nil {
			return nil, fmt.Errorf("get empty-root node: %w", err)
		}

		if raw == nil {
			return emptyRoot(), nil
		}

		return rehashNodeRaw(ctx, db, rootHash, raw, 0, walk)
	}

	return rehashNode(ctx, db, rootHash, 0, walk)
}

func RehashTreeParallel(
	ctx context.Context,
	db nodeGetter,
	rootHash []byte,
	workers int,
	addProgress func(int64),
) ([]byte, error) {
	if workers <= 1 {
		return RehashTree(ctx, db, rootHash)
	}

	if len(rootHash) == 0 {
		return emptyRoot(), nil
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("parallel rehash: %w", err)
	}

	emptyHash := emptyTreeHash()
	if bytes.Equal(rootHash, emptyHash[:]) {
		raw, err := db.Get(append([]byte{nodeKeyPrefix}, rootHash...))
		if err != nil {
			return nil, fmt.Errorf("get empty-root node: %w", err)
		}

		if raw == nil {
			return emptyRoot(), nil
		}
	}

	walker := &parallelWalk{
		db:          db,
		cache:       newShardCache(),
		sem:         make(chan struct{}, workers),
		addProgress: addProgress,
	}

	return walker.rehash(ctx, rootHash, 0)
}

func emptyRoot() []byte {
	out := make([]byte, 32)
	emptyHash := emptyTreeHash()
	copy(out, emptyHash[:])

	return out
}

func emptyTreeHash() [32]byte { return sha256.Sum256(nil) }

func rehashNode(ctx context.Context, db nodeGetter, hash []byte, depth int, walk *sequentialWalk) ([]byte, error) {
	if cached, ok := walk.cache[string(hash)]; ok {
		return cached, nil
	}

	if err := walk.checkCancel(ctx); err != nil {
		return nil, err
	}

	if depth > maxTreeDepth {
		return nil, fmt.Errorf("node chain exceeds %d levels at %x (malformed archive)", maxTreeDepth, hash)
	}

	raw, err := db.Get(append([]byte{nodeKeyPrefix}, hash...))
	if err != nil {
		return nil, fmt.Errorf("get node %x: %w", hash, err)
	}

	if raw == nil {
		return nil, fmt.Errorf("%w: %x", ErrMissingNode, hash)
	}

	return rehashNodeRaw(ctx, db, hash, raw, depth, walk)
}

func rehashNodeRaw(
	ctx context.Context,
	db nodeGetter,
	hash, raw []byte,
	depth int,
	walk *sequentialWalk,
) ([]byte, error) {
	n, err := parseNode(raw)
	if err != nil {
		return nil, err
	}

	var result []byte
	if n.isLeaf {
		result = hashLeaf(n)
	} else {
		lh, err := rehashNode(ctx, db, n.leftHash, depth+1, walk)
		if err != nil {
			return nil, err
		}

		rh, err := rehashNode(ctx, db, n.rightHash, depth+1, walk)
		if err != nil {
			return nil, err
		}

		result = hashInner(n, lh, rh)
	}

	walk.cache[string(hash)] = result

	return result, nil
}

type parallelWalk struct {
	db          nodeGetter
	cache       *shardCache
	sem         chan struct{}
	visited     atomic.Int64
	addProgress func(int64)
}

func (p *parallelWalk) checkCancel(ctx context.Context) error {
	if p.visited.Add(1)%checkInterval == 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("parallel rehash: %w", err)
		}
	}

	return nil
}

func (p *parallelWalk) rehash(ctx context.Context, hash []byte, depth int) ([]byte, error) {
	if v, ok := p.cache.get(hash); ok {
		return v, nil
	}

	if err := p.checkCancel(ctx); err != nil {
		return nil, err
	}

	if depth > maxTreeDepth {
		return nil, fmt.Errorf("node chain exceeds %d levels at %x (malformed archive)", maxTreeDepth, hash)
	}

	raw, err := p.db.Get(append([]byte{nodeKeyPrefix}, hash...))
	if err != nil {
		return nil, fmt.Errorf("get node %x: %w", hash, err)
	}

	if raw == nil {
		return nil, fmt.Errorf("%w: %x", ErrMissingNode, hash)
	}

	n, err := parseNode(raw)
	if err != nil {
		return nil, err
	}

	if p.addProgress != nil {
		p.addProgress(1)
	}

	var result []byte
	if n.isLeaf {
		result = hashLeaf(n)
	} else {
		var (
			lh, rh     []byte
			lerr, rerr error
		)

		select {
		case p.sem <- struct{}{}:
			done := make(chan struct{})

			go func() {
				lh, lerr = p.rehash(ctx, n.leftHash, depth+1)
				<-p.sem
				close(done)
			}()

			rh, rerr = p.rehash(ctx, n.rightHash, depth+1)

			<-done
		default:
			lh, lerr = p.rehash(ctx, n.leftHash, depth+1)
			if lerr == nil {
				rh, rerr = p.rehash(ctx, n.rightHash, depth+1)
			}
		}

		if lerr != nil {
			return nil, lerr
		}

		if rerr != nil {
			return nil, rerr
		}

		result = hashInner(n, lh, rh)
	}

	p.cache.put(hash, result)

	return result, nil
}

type reader struct {
	b   []byte
	pos int
}

func (r *reader) readBytes() ([]byte, error) {
	l, n := binary.Uvarint(r.b[r.pos:])
	if n <= 0 {
		return nil, errors.New("bad byte length")
	}

	r.pos += n

	if l > uint64(len(r.b)-r.pos) {
		return nil, errors.New("byte slice overruns node")
	}

	out := r.b[r.pos : r.pos+int(l)]
	r.pos += int(l)

	return out, nil
}

func (r *reader) readVarint() (int64, error) {
	v, n := binary.Varint(r.b[r.pos:])
	if n <= 0 {
		return 0, errors.New("bad varint")
	}

	r.pos += n

	return v, nil
}

type sequentialWalk struct {
	cache   map[string][]byte
	visited int
}

func (walk *sequentialWalk) checkCancel(ctx context.Context) error {
	walk.visited++
	if walk.visited%checkInterval == 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("rehash tree: %w", err)
		}
	}

	return nil
}
