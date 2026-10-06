// Package slidingcounter provides a goroutine-safe, in-memory sliding
// window counter keyed by string. The window is divided into a fixed
// number of time buckets that rotate and expire automatically as time
// advances.
package slidingcounter

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ErrEmptyKey is returned by Add when the key is an empty string.
var ErrEmptyKey = errors.New("slidingcounter: key must not be empty")

// Entry is a single key/value pair returned by Snapshot.
type Entry struct {
	Key   string
	Value int64
}

// Counter is a concurrent sliding window counter. The zero value is not
// usable; construct one with New.
type Counter struct {
	mu          sync.Mutex
	window      time.Duration
	bucketWidth time.Duration
	buckets     []bucket
	now         func() time.Time
}

// bucket holds the counts collected during one aligned time slot.
// A nil counts map means the bucket currently holds no data.
type bucket struct {
	start  time.Time
	counts map[string]int64
}

// Option configures a Counter.
type Option func(*Counter)

// WithClock overrides the time source used by the Counter. It is mainly
// useful for tests; production code normally relies on the default
// time.Now. A nil function is ignored.
func WithClock(now func() time.Time) Option {
	return func(c *Counter) {
		if now != nil {
			c.now = now
		}
	}
}

// New creates a Counter covering a sliding window of the given total
// duration, split into buckets of bucketWidth. Both durations must be
// positive and window must be an exact multiple of bucketWidth.
func New(window, bucketWidth time.Duration, opts ...Option) (*Counter, error) {
	if window <= 0 {
		return nil, fmt.Errorf("slidingcounter: window must be > 0, got %v", window)
	}
	if bucketWidth <= 0 {
		return nil, fmt.Errorf("slidingcounter: bucketWidth must be > 0, got %v", bucketWidth)
	}
	if window%bucketWidth != 0 {
		return nil, fmt.Errorf("slidingcounter: window %v is not a multiple of bucketWidth %v", window, bucketWidth)
	}
	c := &Counter{
		window:      window,
		bucketWidth: bucketWidth,
		buckets:     make([]bucket, int(window/bucketWidth)),
		now:         time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Window returns the total window duration the Counter was created with.
func (c *Counter) Window() time.Duration { return c.window }

// BucketWidth returns the bucket width the Counter was created with.
func (c *Counter) BucketWidth() time.Duration { return c.bucketWidth }

// Add increases the count for key in the current time bucket by delta.
// Delta may be negative. It returns ErrEmptyKey for an empty key.
func (c *Counter) Add(key string, delta int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	currentStart := c.currentStartLocked()
	c.expireLocked(currentStart)
	b := &c.buckets[c.slot(currentStart)]
	if b.counts == nil || !b.start.Equal(currentStart) {
		b.counts = make(map[string]int64)
		b.start = currentStart
	}
	b.counts[key] += delta
	return nil
}

// Count returns the total count for key across all buckets still inside
// the current sliding window.
func (c *Counter) Count(key string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	currentStart := c.currentStartLocked()
	c.expireLocked(currentStart)
	var total int64
	for i := range c.buckets {
		total += c.buckets[i].counts[key]
	}
	return total
}

// Reset removes every retained count for key from the current window.
// It reports whether the key had any retained count; keys whose data has
// already expired are treated as absent.
func (c *Counter) Reset(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	currentStart := c.currentStartLocked()
	c.expireLocked(currentStart)
	found := false
	for i := range c.buckets {
		if _, ok := c.buckets[i].counts[key]; ok {
			delete(c.buckets[i].counts, key)
			found = true
		}
	}
	return found
}

// Snapshot returns the totals of all keys that still have retained counts
// in the current window, sorted by key in ascending lexicographic order.
// The returned slice is freshly allocated; mutating it does not affect
// the Counter.
func (c *Counter) Snapshot() []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	currentStart := c.currentStartLocked()
	c.expireLocked(currentStart)
	totals := make(map[string]int64)
	for i := range c.buckets {
		for key, value := range c.buckets[i].counts {
			totals[key] += value
		}
	}
	entries := make([]Entry, 0, len(totals))
	for key, value := range totals {
		entries = append(entries, Entry{Key: key, Value: value})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	return entries
}

// Len returns the number of distinct keys that still have retained counts
// in the current window.
func (c *Counter) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	currentStart := c.currentStartLocked()
	c.expireLocked(currentStart)
	keys := make(map[string]struct{})
	for i := range c.buckets {
		for key := range c.buckets[i].counts {
			keys[key] = struct{}{}
		}
	}
	return len(keys)
}

// currentStartLocked returns the aligned start time of the bucket that
// contains the current time.
func (c *Counter) currentStartLocked() time.Time {
	return c.now().Truncate(c.bucketWidth)
}

// expireLocked clears every bucket whose start time falls outside the
// valid range (currentStart-window, currentStart]. Buckets with a start
// time in the future (clock moved backwards) are cleared as well. It must
// be called with c.mu held, before any read or write, so that rotation,
// expiry and the operation itself happen as one consistent step.
func (c *Counter) expireLocked(currentStart time.Time) {
	oldest := currentStart.Add(-c.window)
	for i := range c.buckets {
		b := &c.buckets[i]
		if b.counts == nil {
			continue
		}
		if !b.start.After(oldest) || b.start.After(currentStart) {
			b.counts = nil
			b.start = time.Time{}
		}
	}
}

// slot maps an aligned bucket start time to its fixed bucket index.
func (c *Counter) slot(start time.Time) int {
	n := int64(len(c.buckets))
	v := start.UnixNano() / c.bucketWidth.Nanoseconds() % n
	if v < 0 {
		v += n
	}
	return int(v)
}
