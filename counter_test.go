package slidingcounter

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced Clock for deterministic tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

func newTestCounter(t *testing.T, window, bucketWidth time.Duration) (*Counter, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	c, err := NewCounter(window, bucketWidth, WithClock(clock))
	if err != nil {
		t.Fatalf("NewCounter(%s, %s): %v", window, bucketWidth, err)
	}
	return c, clock
}

func mustAdd(t *testing.T, c *Counter, key string, delta int64) {
	t.Helper()
	if err := c.Add(key, delta); err != nil {
		t.Fatalf("Add(%q, %d): %v", key, delta, err)
	}
}

func TestNewCounterInvalidConfig(t *testing.T) {
	cases := []struct {
		window, bucketWidth time.Duration
	}{
		{0, time.Second},
		{-time.Second, time.Second},
		{time.Second, 0},
		{time.Second, -time.Second},
		{10 * time.Second, 3 * time.Second}, // not a multiple
	}
	for _, tc := range cases {
		if _, err := NewCounter(tc.window, tc.bucketWidth); err == nil {
			t.Errorf("NewCounter(%s, %s) succeeded, want error", tc.window, tc.bucketWidth)
		}
	}

	c, err := NewCounter(10*time.Second, time.Second)
	if err != nil {
		t.Fatalf("NewCounter with valid config: %v", err)
	}
	if c.Window() != 10*time.Second {
		t.Errorf("Window() = %s, want 10s", c.Window())
	}
	if c.BucketWidth() != time.Second {
		t.Errorf("BucketWidth() = %s, want 1s", c.BucketWidth())
	}
}

func TestAddAndCount(t *testing.T) {
	c, _ := newTestCounter(t, 10*time.Second, time.Second)

	mustAdd(t, c, "a", 3)
	mustAdd(t, c, "a", 2)
	mustAdd(t, c, "a", -1)

	if got := c.Count("a"); got != 4 {
		t.Errorf("Count(a) = %d, want 4", got)
	}
	if got := c.Count("missing"); got != 0 {
		t.Errorf("Count(missing) = %d, want 0", got)
	}
}

func TestAddEmptyKey(t *testing.T) {
	c, _ := newTestCounter(t, 10*time.Second, time.Second)

	if err := c.Add("", 1); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("Add(empty) err = %v, want ErrEmptyKey", err)
	}
	if got := c.Len(); got != 0 {
		t.Errorf("Len() = %d after rejected Add, want 0", got)
	}
}

func TestMultipleKeys(t *testing.T) {
	c, _ := newTestCounter(t, 10*time.Second, time.Second)

	mustAdd(t, c, "a", 1)
	mustAdd(t, c, "b", 2)
	mustAdd(t, c, "a", 4)

	if got := c.Count("a"); got != 5 {
		t.Errorf("Count(a) = %d, want 5", got)
	}
	if got := c.Count("b"); got != 2 {
		t.Errorf("Count(b) = %d, want 2", got)
	}
	if got := c.Len(); got != 2 {
		t.Errorf("Len() = %d, want 2", got)
	}
}

func TestBucketRotationAccumulates(t *testing.T) {
	c, clock := newTestCounter(t, 10*time.Second, time.Second)

	mustAdd(t, c, "a", 1)
	clock.Advance(time.Second)
	mustAdd(t, c, "a", 2)
	clock.Advance(2 * time.Second) // skip one bucket entirely
	mustAdd(t, c, "a", 4)

	if got := c.Count("a"); got != 7 {
		t.Errorf("Count(a) = %d, want 7 across rotated buckets", got)
	}
}

func TestWindowExpiry(t *testing.T) {
	c, clock := newTestCounter(t, 10*time.Second, time.Second)

	mustAdd(t, c, "a", 5)

	// 9s later the original bucket is still the oldest valid one.
	clock.Advance(9 * time.Second)
	if got := c.Count("a"); got != 5 {
		t.Errorf("Count(a) after 9s = %d, want 5", got)
	}

	// At 10s the original bucket falls out of the window.
	clock.Advance(time.Second)
	if got := c.Count("a"); got != 0 {
		t.Errorf("Count(a) after 10s = %d, want 0", got)
	}
	if got := c.Len(); got != 0 {
		t.Errorf("Len() after expiry = %d, want 0", got)
	}

	// New writes still work after the old bucket expired.
	mustAdd(t, c, "a", 7)
	if got := c.Count("a"); got != 7 {
		t.Errorf("Count(a) after new Add = %d, want 7", got)
	}
}

func TestTimeJumpBeyondWindow(t *testing.T) {
	c, clock := newTestCounter(t, 10*time.Second, time.Second)

	mustAdd(t, c, "a", 5)
	mustAdd(t, c, "b", 3)

	// Jump far beyond the whole window: everything must be dropped.
	clock.Advance(time.Hour)
	if got := c.Count("a"); got != 0 {
		t.Errorf("Count(a) after 1h jump = %d, want 0", got)
	}
	if got := c.Len(); got != 0 {
		t.Errorf("Len() after 1h jump = %d, want 0", got)
	}
	if got := c.Snapshot(); len(got) != 0 {
		t.Errorf("Snapshot() after 1h jump = %v, want empty", got)
	}

	// The counter keeps working; stale data must not reappear.
	mustAdd(t, c, "a", 2)
	if got := c.Count("a"); got != 2 {
		t.Errorf("Count(a) after jump and new Add = %d, want 2", got)
	}
}

func TestReset(t *testing.T) {
	c, clock := newTestCounter(t, 10*time.Second, time.Second)

	mustAdd(t, c, "a", 1)
	clock.Advance(time.Second)
	mustAdd(t, c, "a", 2)
	mustAdd(t, c, "b", 3)

	if !c.Reset("a") {
		t.Error("Reset(a) = false, want true")
	}
	if got := c.Count("a"); got != 0 {
		t.Errorf("Count(a) after Reset = %d, want 0", got)
	}
	if got := c.Count("b"); got != 3 {
		t.Errorf("Count(b) after Reset(a) = %d, want 3", got)
	}
	if c.Reset("a") {
		t.Error("Reset(a) again = true, want false")
	}
	if c.Reset("missing") {
		t.Error("Reset(missing) = true, want false")
	}
}

func TestSnapshotSortedAndIsolated(t *testing.T) {
	c, clock := newTestCounter(t, 10*time.Second, time.Second)

	mustAdd(t, c, "b", 2)
	mustAdd(t, c, "c", 3)
	clock.Advance(time.Second)
	mustAdd(t, c, "a", 1)
	mustAdd(t, c, "b", 5) // spans two buckets: 2 + 5

	got := c.Snapshot()
	want := []Entry{{"a", 1}, {"b", 7}, {"c", 3}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Snapshot() = %v, want %v", got, want)
	}

	// Mutating the returned slice must not corrupt internal state.
	got[0].Value = 999
	got[1].Key = "zzz"
	if c.Count("a") != 1 || c.Count("b") != 7 {
		t.Error("mutating Snapshot result changed internal state")
	}
}

func TestLenTracksExpiry(t *testing.T) {
	c, clock := newTestCounter(t, 5*time.Second, time.Second)

	mustAdd(t, c, "a", 1)
	mustAdd(t, c, "b", 1)
	clock.Advance(time.Second)
	mustAdd(t, c, "c", 1)
	if got := c.Len(); got != 3 {
		t.Errorf("Len() = %d, want 3", got)
	}

	// After 4 more seconds the bucket holding a and b has expired;
	// only c (added one second later) is still inside the window.
	clock.Advance(4 * time.Second)
	if got := c.Len(); got != 1 {
		t.Errorf("Len() after expiry = %d, want 1", got)
	}
}

func TestConcurrentAccess(t *testing.T) {
	c, err := NewCounter(10*time.Second, time.Second)
	if err != nil {
		t.Fatalf("NewCounter: %v", err)
	}

	const writers = 8
	const addsPerWriter = 100

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			for i := 0; i < addsPerWriter; i++ {
				if err := c.Add(key, 1); err != nil {
					t.Errorf("Add: %v", err)
					return
				}
			}
		}(fmt.Sprintf("key-%d", w))
	}

	// Concurrent readers must not race with the writers.
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = c.Count("key-0")
					_ = c.Snapshot()
					_ = c.Len()
				}
			}
		}()
	}

	wg.Wait()
	close(stop)
	readers.Wait()

	for w := 0; w < writers; w++ {
		key := fmt.Sprintf("key-%d", w)
		if got := c.Count(key); got != addsPerWriter {
			t.Errorf("Count(%s) = %d, want %d", key, got, addsPerWriter)
		}
	}
	if got := c.Len(); got != writers {
		t.Errorf("Len() = %d, want %d", got, writers)
	}
	if got := c.Snapshot(); len(got) != writers {
		t.Errorf("len(Snapshot()) = %d, want %d", len(got), writers)
	}
}
