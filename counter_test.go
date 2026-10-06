package slidingcounter_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"slidingcounter"
)

// fakeClock is a manually advanced clock used to make time-based
// behavior deterministic in tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_700_000_000, 0)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func newTestCounter(t *testing.T, window, bucketWidth time.Duration, clock *fakeClock) *slidingcounter.Counter {
	t.Helper()
	c, err := slidingcounter.New(window, bucketWidth, slidingcounter.WithClock(clock.Now))
	if err != nil {
		t.Fatalf("New(%v, %v) returned unexpected error: %v", window, bucketWidth, err)
	}
	return c
}

func mustAdd(t *testing.T, c *slidingcounter.Counter, key string, delta int64) {
	t.Helper()
	if err := c.Add(key, delta); err != nil {
		t.Fatalf("Add(%q, %d) returned unexpected error: %v", key, delta, err)
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name        string
		window      time.Duration
		bucketWidth time.Duration
	}{
		{"zero window", 0, time.Second},
		{"negative window", -time.Second, time.Second},
		{"zero bucket width", time.Second, 0},
		{"negative bucket width", time.Second, -time.Second},
		{"window not a multiple of bucket width", 3 * time.Second, 2 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := slidingcounter.New(tc.window, tc.bucketWidth); err == nil {
				t.Fatalf("New(%v, %v) succeeded, want error", tc.window, tc.bucketWidth)
			}
		})
	}
}

func TestWindowAndBucketWidth(t *testing.T) {
	c := newTestCounter(t, time.Minute, 10*time.Second, newFakeClock())
	if got := c.Window(); got != time.Minute {
		t.Errorf("Window() = %v, want %v", got, time.Minute)
	}
	if got := c.BucketWidth(); got != 10*time.Second {
		t.Errorf("BucketWidth() = %v, want %v", got, 10*time.Second)
	}
}

func TestAddAndCount(t *testing.T) {
	clock := newFakeClock()
	c := newTestCounter(t, time.Minute, 10*time.Second, clock)
	mustAdd(t, c, "hits", 3)
	mustAdd(t, c, "hits", 4)
	mustAdd(t, c, "hits", -2)
	if got := c.Count("hits"); got != 5 {
		t.Errorf("Count(hits) = %d, want 5", got)
	}
	if got := c.Count("missing"); got != 0 {
		t.Errorf("Count(missing) = %d, want 0", got)
	}
}

func TestAddRejectsEmptyKey(t *testing.T) {
	c := newTestCounter(t, time.Minute, 10*time.Second, newFakeClock())
	if err := c.Add("", 1); !errors.Is(err, slidingcounter.ErrEmptyKey) {
		t.Errorf("Add(empty) error = %v, want ErrEmptyKey", err)
	}
	if got := c.Len(); got != 0 {
		t.Errorf("Len() = %d, want 0 after rejected Add", got)
	}
}

func TestMultipleKeysAreIndependent(t *testing.T) {
	clock := newFakeClock()
	c := newTestCounter(t, time.Minute, 10*time.Second, clock)
	mustAdd(t, c, "a", 1)
	mustAdd(t, c, "b", 10)
	mustAdd(t, c, "a", 2)
	if got := c.Count("a"); got != 3 {
		t.Errorf("Count(a) = %d, want 3", got)
	}
	if got := c.Count("b"); got != 10 {
		t.Errorf("Count(b) = %d, want 10", got)
	}
	if got := c.Len(); got != 2 {
		t.Errorf("Len() = %d, want 2", got)
	}
}

func TestBucketRotationAccumulatesAcrossBuckets(t *testing.T) {
	clock := newFakeClock()
	c := newTestCounter(t, 30*time.Second, 10*time.Second, clock)
	mustAdd(t, c, "a", 1) // bucket at t0
	clock.Advance(10 * time.Second)
	mustAdd(t, c, "a", 2) // bucket at t0+10s
	clock.Advance(10 * time.Second)
	mustAdd(t, c, "a", 3) // bucket at t0+20s
	if got := c.Count("a"); got != 6 {
		t.Errorf("Count(a) = %d, want 6 across three buckets", got)
	}
}

func TestWindowExpiryDropsOldestBucket(t *testing.T) {
	clock := newFakeClock()
	c := newTestCounter(t, 30*time.Second, 10*time.Second, clock)
	mustAdd(t, c, "a", 1) // t0
	clock.Advance(10 * time.Second)
	mustAdd(t, c, "a", 2) // t0+10s
	clock.Advance(10 * time.Second)
	mustAdd(t, c, "a", 3)           // t0+20s
	clock.Advance(10 * time.Second) // now t0+30s: the t0 bucket has left the window
	if got := c.Count("a"); got != 5 {
		t.Errorf("Count(a) = %d, want 5 after the oldest bucket expired", got)
	}
	mustAdd(t, c, "a", 4) // reuses the slot previously held by the expired t0 bucket
	if got := c.Count("a"); got != 9 {
		t.Errorf("Count(a) = %d, want 9 after writing into the rotated bucket", got)
	}
}

func TestTimeJumpBeyondWindowDropsEverything(t *testing.T) {
	clock := newFakeClock()
	c := newTestCounter(t, 30*time.Second, 10*time.Second, clock)
	mustAdd(t, c, "a", 5)
	mustAdd(t, c, "b", 7)
	clock.Advance(time.Hour) // far beyond the whole window
	if got := c.Count("a"); got != 0 {
		t.Errorf("Count(a) = %d, want 0 after a jump beyond the window", got)
	}
	if got := c.Len(); got != 0 {
		t.Errorf("Len() = %d, want 0 after a jump beyond the window", got)
	}
	mustAdd(t, c, "a", 2) // the counter keeps working normally afterwards
	if got := c.Count("a"); got != 2 {
		t.Errorf("Count(a) = %d, want 2 after adding past the jump", got)
	}
}

func TestReset(t *testing.T) {
	clock := newFakeClock()
	c := newTestCounter(t, time.Minute, 10*time.Second, clock)
	mustAdd(t, c, "a", 3)
	mustAdd(t, c, "b", 4)
	if !c.Reset("a") {
		t.Error("Reset(a) = false, want true for an existing key")
	}
	if got := c.Count("a"); got != 0 {
		t.Errorf("Count(a) = %d, want 0 after Reset", got)
	}
	if got := c.Count("b"); got != 4 {
		t.Errorf("Count(b) = %d, want 4; Reset must not affect other keys", got)
	}
	if c.Reset("a") {
		t.Error("Reset(a) = true, want false after the key was cleared")
	}
	if c.Reset("missing") {
		t.Error("Reset(missing) = true, want false for an unknown key")
	}
}

func TestSnapshotIsSortedAndIndependent(t *testing.T) {
	clock := newFakeClock()
	c := newTestCounter(t, time.Minute, 10*time.Second, clock)
	mustAdd(t, c, "banana", 2)
	mustAdd(t, c, "apple", 1)
	mustAdd(t, c, "cherry", 3)
	clock.Advance(10 * time.Second)
	mustAdd(t, c, "apple", 4) // totals span multiple buckets

	entries := c.Snapshot()
	want := []slidingcounter.Entry{
		{Key: "apple", Value: 5},
		{Key: "banana", Value: 2},
		{Key: "cherry", Value: 3},
	}
	if len(entries) != len(want) {
		t.Fatalf("Snapshot() returned %d entries, want %d", len(entries), len(want))
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("Snapshot()[%d] = %+v, want %+v", i, entries[i], want[i])
		}
	}

	entries[0].Value = 999 // mutating the result must not corrupt the counter
	if got := c.Count("apple"); got != 5 {
		t.Errorf("Count(apple) = %d, want 5 after mutating the snapshot result", got)
	}
}

func TestLenTracksLiveKeys(t *testing.T) {
	clock := newFakeClock()
	c := newTestCounter(t, 30*time.Second, 10*time.Second, clock)
	if got := c.Len(); got != 0 {
		t.Errorf("Len() = %d, want 0 for an empty counter", got)
	}
	mustAdd(t, c, "a", 1)
	mustAdd(t, c, "b", 1)
	clock.Advance(10 * time.Second)
	mustAdd(t, c, "c", 1)
	if got := c.Len(); got != 3 {
		t.Errorf("Len() = %d, want 3", got)
	}
	clock.Advance(time.Hour) // everything expires
	if got := c.Len(); got != 0 {
		t.Errorf("Len() = %d, want 0 after all data expired", got)
	}
}

func TestConcurrentAccess(t *testing.T) {
	clock := newFakeClock()
	c := newTestCounter(t, time.Minute, 10*time.Second, clock)

	const workers = 8
	const addsPerWorker = 100
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", id%4)
			for i := 0; i < addsPerWorker; i++ {
				if err := c.Add(key, 1); err != nil {
					t.Errorf("Add(%q, 1) failed: %v", key, err)
					return
				}
				_ = c.Count(key)
				_ = c.Snapshot()
				_ = c.Len()
			}
		}(w)
	}
	wg.Wait()

	const wantTotal = workers * addsPerWorker
	var total int64
	for _, e := range c.Snapshot() {
		total += e.Value
	}
	if total != wantTotal {
		t.Errorf("snapshot total = %d, want %d", total, wantTotal)
	}
	if got := c.Len(); got != 4 {
		t.Errorf("Len() = %d, want 4", got)
	}
}
