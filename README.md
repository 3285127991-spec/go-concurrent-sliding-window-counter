# slidingcounter

一个纯 Go 实现的并发安全滑动窗口计数器，仅依赖标准库。

窗口被划分为固定数量的时间桶（`window / bucketWidth` 个），随时间自动轮转；
过期桶在复用前被清空，超出窗口的数据不会重新计入。一次超过整个窗口的时间
跳跃会直接丢弃全部过期数据。窗口边界是确定的：只统计当前桶以及之前
`window/bucketWidth - 1` 个仍有效的桶。

## API

```go
c, err := slidingcounter.NewCounter(10*time.Second, time.Second) // window 必须能被 bucketWidth 整除，且都 > 0
```

- `Add(key string, delta int64) error`：在当前时间桶为 key 累加 delta；空 key 返回 `ErrEmptyKey`。
- `Count(key string) int64`：返回 key 在有效窗口内的累计值。
- `Reset(key string) bool`：清除 key 仍保留的全部计数，返回是否有内容被清除。
- `Snapshot() []Entry`：返回所有仍有有效数据的 key 及累计值，按 key 字典序排列；结果为独立副本。
- `Len() int`：仍有有效计数的 key 数量。
- `Window() / BucketWidth() time.Duration`：返回构造时的窗口与桶宽。

所有方法都可被多个 goroutine 安全并发调用。测试可通过
`slidingcounter.WithClock(clock)` 注入可控时钟（实现 `Clock` 接口即可）。

## 运行测试

```sh
go test -race ./...
```

## 运行演示

```sh
go run ./cmd/demo
```
