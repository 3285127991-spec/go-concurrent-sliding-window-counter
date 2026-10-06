# slidingcounter

A goroutine-safe, in-memory sliding window counter for Go. The window is
split into a fixed number of time buckets that rotate and expire
automatically as time advances. No third-party dependencies.

## Features

- Fixed number of buckets: `window / bucketWidth` (must divide exactly).
- Deterministic window boundary: counts the current bucket plus the
  previous `window/bucketWidth - 1` buckets, nothing older.
- Old buckets are cleared before reuse; a time jump larger than the whole
  window simply drops everything.
- Safe for concurrent `Add`, `Count`, `Reset`, `Snapshot` and `Len` from
  multiple goroutines.
- Injectable clock via `WithClock` for deterministic tests.

## API

- `New(window, bucketWidth time.Duration, opts ...Option) (*Counter, error)`
- `Add(key string, delta int64) error` (rejects empty keys with `ErrEmptyKey`)
- `Count(key string) int64`
- `Reset(key string) bool` (false when the key has no retained counts)
- `Snapshot() []Entry` (sorted by key; safe for the caller to mutate)
- `Len() int`
- `Window() time.Duration` / `BucketWidth() time.Duration`

## Example

```go
c, err := slidingcounter.New(time.Minute, 10*time.Second)
if err != nil {
	log.Fatal(err)
}
_ = c.Add("requests", 1)
fmt.Println(c.Count("requests"))
```

## Run the demo

```sh
go run ./cmd/demo
```

## Run the tests

```sh
go test -race ./...
```
