# task26100546

## perfana — 游戏构建性能分析库

Go 库，供上层直接调用，采样与分析数据全部保存在进程内存中。
导入一次帧采样后，可反复查询任意时间区间的 1% low FPS 及是否低于目标 FPS 的 70%。

### 用法

```go
frames := []perfana.Frame{
    {CompletionTime: 16_000, Duration: 15_000}, // 完成时间、帧耗时，单位微秒
    // ... 按完成时间非递减排列
}
a, err := perfana.Import(frames, perfana.TargetFrameTime60FPS) // 或 TargetFrameTime30FPS
if err != nil { /* 参数非法 */ }

res := a.Query(start, end) // 区间 [start, end)，仅按完成时间判定
// res.Frames / res.K / res.LowFPS / res.HasMetric / res.Flagged
```

### 规则

- 区间含起点、不含终点；帧完成时间落入区间即计入完整耗时，不裁剪。
- n 帧时取耗时最大的 k = ceil(n/100) 帧，1% low FPS = 1,000,000·k / 这 k 帧耗时总和；
  边界并列不扩大取样数量。
- 目标 FPS = 1,000,000 / 目标帧耗时；1% low FPS 严格低于目标 FPS 的 70% 时标记，
  判定基于整数运算（`10·k·T < 7·Σ`），不受展示精度影响；空区间无指标、不标记。

### 实现

- 归并排序树（merge-sort tree）：节点保存覆盖区间内升序耗时及前缀和，
  查询时二分第 k 大耗时，再求 top-k 总和，单次查询 O(log²n · logD)。
- 单份采样 1,000,000 帧：导入 < 1s，20,000 次查询 < 1s，峰值内存 < 512 MiB。

### 测试

```sh
go test ./...              # 正确性测试（含阈值边界、99/100/101 尾部取样）
go test -run TestScale -v  # 规模测试：报告导入/查询耗时与峰值内存
go test -short ./...       # 跳过规模测试
```
