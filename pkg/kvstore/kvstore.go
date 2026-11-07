package kvstore

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrKeyNotFound    = errors.New("kvstore: key not found")
	ErrUpdateRejected = errors.New("kvstore: update rejected by conflict resolution")
	ErrKeyExpired     = errors.New("kvstore: key has expired")
)

// ValueTuple encapsulates OpenR's versioned key envelope.
type ValueTuple struct {
	Key          string        `json:"key"`
	Version      uint64        `json:"version"`
	OriginatorID string        `json:"originator_id"`
	TTL          time.Duration `json:"ttl"`
	TTLVersion   uint32        `json:"ttl_version"`
	ExpiresAt    time.Time     `json:"expires_at"`
	Hash         uint64        `json:"hash"`
	Data         []byte        `json:"data"`
}

// DeltaEvent is broadcast to subscribers upon local or remote key modifications.
type DeltaEventType string

const (
	EventPut    DeltaEventType = "PUT"
	EventDelete DeltaEventType = "DELETE"
	EventExpire DeltaEventType = "EXPIRE"
)

type DeltaEvent struct {
	Type  DeltaEventType
	Entry ValueTuple
}

type subscription struct {
	ch     chan DeltaEvent
	prefix string
	once   sync.Once
}

func (s *subscription) close() {
	s.once.Do(func() {
		close(s.ch)
	})
}

// KvStore is an in-memory, eventually consistent, thread-safe replicated key-value engine.
type KvStore struct {
	nodeID      string
	mu          sync.RWMutex
	store       map[string]ValueTuple
	subscribers map[*subscription]struct{}
	subMu       sync.RWMutex
	registry    *ModelRegistry
	stopCh      chan struct{}
}

// NewKvStore initializes a KvStore instance with an ephemeral key expiration sweeper.
func NewKvStore(nodeID string, sweepInterval time.Duration) *KvStore {
	kv := &KvStore{
		nodeID:      nodeID,
		store:       make(map[string]ValueTuple),
		subscribers: make(map[*subscription]struct{}),
		registry:    NewModelRegistry(),
		stopCh:      make(chan struct{}),
	}

	if sweepInterval <= 0 {
		sweepInterval = 500 * time.Millisecond
	}
	go kv.runSweeper(sweepInterval)
	return kv
}

func (kv *KvStore) NodeID() string {
	return kv.nodeID
}

func (kv *KvStore) Registry() *ModelRegistry {
	return kv.registry
}

func (kv *KvStore) Close() {
	close(kv.stopCh)
	kv.subMu.Lock()
	defer kv.subMu.Unlock()
	for sub := range kv.subscribers {
		sub.close()
	}
	kv.subscribers = make(map[*subscription]struct{})
}

// ComputeHash generates a 64-bit fingerprint of the payload data.
func ComputeHash(data []byte) uint64 {
	sum := sha256.Sum256(data)
	return binary.BigEndian.Uint64(sum[:8])
}

// CompareOrder implements OpenR's deterministic total ordering.
// Returns >0 if newTuple wins, <0 if existing wins, 0 if identical.
func CompareOrder(newTuple, existing ValueTuple) int {
	if newTuple.Version > existing.Version {
		return 1
	}
	if newTuple.Version < existing.Version {
		return -1
	}

	// Tie-break 1: Lexicographical comparison of OriginatorID
	if newTuple.OriginatorID > existing.OriginatorID {
		return 1
	}
	if newTuple.OriginatorID < existing.OriginatorID {
		return -1
	}

	// Tie-break 2: Data hash
	if newTuple.Hash > existing.Hash {
		return 1
	}
	if newTuple.Hash < existing.Hash {
		return -1
	}

	return 0
}

// Put writes or updates a key locally. Automatically increments Version and computes Hash.
func (kv *KvStore) Put(key string, data []byte, ttl time.Duration) (ValueTuple, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	var nextVersion uint64 = 1
	var nextTTLVersion uint32 = 1

	existing, exists := kv.store[key]
	if exists {
		nextVersion = existing.Version + 1
		nextTTLVersion = existing.TTLVersion + 1
	}

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	entry := ValueTuple{
		Key:          key,
		Version:      nextVersion,
		OriginatorID: kv.nodeID,
		TTL:          ttl,
		TTLVersion:   nextTTLVersion,
		ExpiresAt:    expiresAt,
		Hash:         ComputeHash(data),
		Data:         data,
	}

	kv.store[key] = entry
	kv.broadcast(DeltaEvent{Type: EventPut, Entry: entry})
	return entry, nil
}

// MergeUpdate applies an incoming delta update from a peer router using OpenR conflict resolution.
func (kv *KvStore) MergeUpdate(remote ValueTuple) (bool, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	now := time.Now()
	if remote.TTL > 0 && !remote.ExpiresAt.IsZero() && remote.ExpiresAt.Before(now) {
		return false, ErrKeyExpired
	}

	existing, exists := kv.store[remote.Key]
	if !exists {
		kv.store[remote.Key] = remote
		kv.broadcast(DeltaEvent{Type: EventPut, Entry: remote})
		return true, nil
	}

	cmp := CompareOrder(remote, existing)
	if cmp > 0 {
		kv.store[remote.Key] = remote
		kv.broadcast(DeltaEvent{Type: EventPut, Entry: remote})
		return true, nil
	}

	// If identical, check if it's a TTL refresh (ttlVersion bumped by originator)
	if cmp == 0 && remote.TTLVersion > existing.TTLVersion && remote.OriginatorID == existing.OriginatorID {
		existing.TTLVersion = remote.TTLVersion
		existing.ExpiresAt = remote.ExpiresAt
		kv.store[remote.Key] = existing
		return true, nil
	}

	return false, ErrUpdateRejected
}

// Get retrieves an active key entry.
func (kv *KvStore) Get(key string) (ValueTuple, error) {
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	entry, exists := kv.store[key]
	if !exists {
		return ValueTuple{}, ErrKeyNotFound
	}

	if entry.TTL > 0 && !entry.ExpiresAt.IsZero() && time.Now().After(entry.ExpiresAt) {
		return ValueTuple{}, ErrKeyExpired
	}

	return entry, nil
}

// Delete marks and removes a key.
func (kv *KvStore) Delete(key string) bool {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	entry, exists := kv.store[key]
	if !exists {
		return false
	}

	delete(kv.store, key)
	kv.broadcast(DeltaEvent{Type: EventDelete, Entry: entry})
	return true
}

// Snapshot returns a copy of all active keys matching an optional prefix.
func (kv *KvStore) Snapshot(prefix string) map[string]ValueTuple {
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	now := time.Now()
	res := make(map[string]ValueTuple)
	for k, v := range kv.store {
		if prefix != "" && len(k) >= len(prefix) && k[:len(prefix)] != prefix {
			continue
		}
		if v.TTL > 0 && !v.ExpiresAt.IsZero() && now.After(v.ExpiresAt) {
			continue
		}
		res[k] = v
	}
	return res
}

// MergeSnapshot executes OpenR's 3-way synchronization, returning keys updated locally.
func (kv *KvStore) MergeSnapshot(remoteSnapshot map[string]ValueTuple) ([]string, error) {
	var applied []string
	for _, remoteTuple := range remoteSnapshot {
		merged, err := kv.MergeUpdate(remoteTuple)
		if err == nil && merged {
			applied = append(applied, remoteTuple.Key)
		}
	}
	return applied, nil
}

// Subscribe listens for live delta updates. Returns a read-only channel.
func (kv *KvStore) Subscribe(ctx context.Context, prefixFilter string) <-chan DeltaEvent {
	sub := &subscription{
		ch:     make(chan DeltaEvent, 128),
		prefix: prefixFilter,
	}

	kv.subMu.Lock()
	kv.subscribers[sub] = struct{}{}
	kv.subMu.Unlock()

	go func() {
		<-ctx.Done()
		kv.subMu.Lock()
		delete(kv.subscribers, sub)
		kv.subMu.Unlock()
		sub.close()
	}()

	return sub.ch
}

func (kv *KvStore) broadcast(ev DeltaEvent) {
	kv.subMu.RLock()
	defer kv.subMu.RUnlock()

	for sub := range kv.subscribers {
		if sub.prefix != "" && len(ev.Entry.Key) >= len(sub.prefix) && ev.Entry.Key[:len(sub.prefix)] != sub.prefix {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
			// Non-blocking drop to prevent slow consumer lag from holding memory lock
		}
	}
}

// runSweeper purges expired keys periodically.
func (kv *KvStore) runSweeper(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-kv.stopCh:
			return
		case now := <-ticker.C:
			kv.sweepExpired(now)
		}
	}
}

func (kv *KvStore) sweepExpired(now time.Time) {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	for k, v := range kv.store {
		if v.TTL > 0 && !v.ExpiresAt.IsZero() && now.After(v.ExpiresAt) {
			delete(kv.store, k)
			kv.broadcast(DeltaEvent{Type: EventExpire, Entry: v})
		}
	}
}

// Convenience Model Helpers stored under keys "model:<provider_id>:<model_id>"

func ModelKeyPrefix(providerID, modelID string) string {
	return fmt.Sprintf("model:%s:%s", providerID, modelID)
}

func MetricKeyPrefix(providerID, modelID string) string {
	return fmt.Sprintf("metric:%s:%s", providerID, modelID)
}

func (kv *KvStore) PublishModel(m Model, ttl time.Duration) error {
	data, err := m.Marshal()
	if err != nil {
		return err
	}
	key := ModelKeyPrefix(m.ProviderID, m.ID)
	_, err = kv.Put(key, data, ttl)
	if err == nil {
		kv.registry.RegisterModel(m)
	}
	return err
}
