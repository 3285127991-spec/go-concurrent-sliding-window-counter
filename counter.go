// Package slidingcounter provides a goroutine-safe, in-memory sliding
// window counter. The window is divided into a fixed number of buckets
// that rotate automatically as time advances; expired buckets are
// discarded lazily and never counted again.
package slidingcounter

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ErrEmptyKey is returned by Add when the provided key is empty.
var ErrEmptyKey = errors.New("slidingcounter: key must not be empty")

// Clock abstracts the time source. The zero-value Counter uses the system
// clock; tests can inject a controllable clock via WithClock.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Option customizes a Counter at construction time.
type Option func(*Counter)

// WithClock sets the time source used by the Counter. A nil clock is
// ignored and the system clock is kept.
func WithClock(c Clock) Option {
	return func(ct *Counter) {
		if c != nil {
			ct.clock = c
		}
	}
}

// Entry is a single key/value pair returned by Snapshot.
type Entry struct {
	Key   string
	Value int64
}

// bucket holds the counts for one aligned time slice. start is the
// bucketWidth-aligned instant the slice begins; counts maps key to the
// accumulated value inside this slice.
type bucket struct {
	start  time.Time
	counts map[string]int64
}

// Counter is a concurrent sliding window counter. All methods are safe to
// call from multiple goroutines; bucket rotation, expiry and mutation are
// performed as one consistent critical section.
type Counter struct {
	window      time.Duration
	bucketWidth time.Duration
	numBuckets  int
	clock       Clock

	mu      sync.Mutex
	buckets []bucket
}

// NewCounter creates a Counter covering a sliding window of the given
// total length, split into window/bucketWidth buckets. Both durations
// must be positive and window must be an exact multiple of bucketWidth.
func NewCounter(window, bucketWidth time.Duration, opts ...Option) (*Counter, error) {
	if window <= 0 {
		return nil, fmt.Errorf("slidingcounter: window must be > 0, got %s", window)
	}
	if bucketWidth <= 0 {
		return nil, fmt.Errorf("slidingcounter: bucketWidth must be > 0, got %s", bucketWidth)
	}
	if window%bucketWidth != 0 {
		return nil, fmt.Errorf("slidingcounter: window %s is not a multiple of bucketWidth %s", window, bucketWidth)
	}
	n := int(window / bucketWidth)
	c := &Counter{
		window:      window,
		bucketWidth: bucketWidth,
		numBuckets:  n,
		clock:       realClock{},
		buckets:     make([]bucket, n),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Window returns the total sliding window length.
func (c *Counter) Window() time.Duration { return c.window }

// BucketWidth returns the width of a single bucket.
func (c *Counter) BucketWidth() time.Duration { return c.bucketWidth }

// Add increases the count for key in the current time bucket by delta.
// It returns ErrEmptyKey if key is empty.
func (c *Counter) Add(key string, delta int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.currentBucket()
	v := b.counts[key] + delta
	if v == 0 {
		delete(b.counts, key)
	} else {
		b.counts[key] = v
	}
	return nil
}

// Count returns the accumulated value for key across all buckets that are
// still inside the sliding window.
func (c *Counter) Count(key string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.currentStart()
	var sum int64
	for i := range c.buckets {
		b := &c.buckets[i]
		if b.counts == nil || !c.valid(b.start, now) {
			continue
		}
		sum += b.counts[key]
	}
	return sum
}

// Reset removes all retained counts for key, including ones already
// expired but not yet reused. It reports whether anything was removed.
func (c *Counter) Reset(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	found := false
	for i := range c.buckets {
		if _, ok := c.buckets[i].counts[key]; ok {
			delete(c.buckets[i].counts, key)
			found = true
		}
	}
	return found
}

// Snapshot returns every key that still has data inside the sliding
// window together with its accumulated value, sorted by key. The result
// is a freshly allocated slice and shares no state with the Counter.
func (c *Counter) Snapshot() []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.currentStart()
	totals := make(map[string]int64)
	for i := range c.buckets {
		b := &c.buckets[i]
		if b.counts == nil || !c.valid(b.start, now) {
			continue
		}
		for k, v := range b.counts {
			totals[k] += v
		}
	}
	entries := make([]Entry, 0, len(totals))
	for k, v := range totals {
		entries = append(entries, Entry{Key: k, Value: v})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	return entries
}

// Len returns the number of keys that still have data inside the sliding
// window.
func (c *Counter) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.currentStart()
	keys := make(map[string]struct{})
	for i := range c.buckets {
		b := &c.buckets[i]
		if b.counts == nil || !c.valid(b.start, now) {
			continue
		}
		for k := range b.counts {
			keys[k] = struct{}{}
		}
	}
	return len(keys)
}

// currentStart returns the bucketWidth-aligned start of the time slice
// the clock is currently in. Callers must hold c.mu.
func (c *Counter) currentStart() time.Time {
	return c.clock.Now().Truncate(c.bucketWidth)
}

// currentBucket returns the bucket for the current time slice, clearing
// any stale contents first so expired data is never counted again.
// Callers must hold c.mu.
func (c *Counter) currentBucket() *bucket {
	start := c.currentStart()
	epoch := start.UnixNano() / int64(c.bucketWidth)
	idx := int(epoch % int64(c.numBuckets))
	if idx < 0 {
		idx += c.numBuckets
	}
	b := &c.buckets[idx]
	if !b.start.Equal(start) {
		b.start = start
		b.counts = make(map[string]int64)
	}
	return b
}

// valid reports whether a bucket holding the slice beginning at start is
// still inside the window relative to the current slice start. The window
// covers the current bucket and the numBuckets-1 slices before it.
func (c *Counter) valid(start, currentStart time.Time) bool {
	oldest := currentStart.Add(c.bucketWidth - c.window)
	return !start.Before(oldest) && !start.After(currentStart)
}
