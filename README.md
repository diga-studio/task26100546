# frameperf

游戏构建性能分析库（Go）：导入一次帧采样到进程内存，随后可反复查询任意时间区间的
1% low FPS 指标及是否低于目标 70% 的结论。

## 用法

```go
import "frameperf/frameperf"

// frames 按完成时间非递减排列；目标帧耗时固定为 16600 或 33300 微秒
s, err := frameperf.NewSample(frames, frameperf.TargetFrameTime60FPS)
if err != nil { /* ... */ }

// 查询区间 [start, end)，含起点不含终点，按帧完成时间判定
res := s.Query(startUs, endUs)
if res.HasData {
    // res.Frames      区间内帧数 n
    // res.Sampled     尾部取样数 k = ceil(n/100)
    // res.DurationSum 耗时最大的 k 帧耗时总和（微秒）
    // res.LowFPS      1% low FPS = 1_000_000 * k / DurationSum（展示用）
    // res.Flagged     LowFPS 严格低于目标 FPS 的 70%（按未舍入整数判定）
}
```

## 规则要点

- 区间仅按帧完成时间判定归属，帧耗时完整计入、不裁剪。
- 相同完成时间的记录视为不同帧；耗时并列时仍只取 k 帧。
- 空区间 `HasData=false`，不产生低帧率指标，也不标记。
- 70% 阈值判定使用整数运算（`10*k*target < 7*sum`），不受展示精度影响。

## 实现与规模

- 帧耗时坐标压缩后建立小波树（wavelet tree），单次区间查询 O(log σ)。
- 规模测试（`go test ./frameperf/ -run TestScale -v`，可重复执行）实测：
  1,000,000 帧导入约 0.27s（限制 20s）；20,000 次查询共约 17ms（限制 5s），
  其中 10,000 个区间各含不少于 200,000 帧；进程峰值常驻内存约 226 MiB（限制 512 MiB）。

## 测试

```sh
go test ./...
```

覆盖：随机小规模区间与独立直接计算逐一比对、重复完成时间、起止边界、
重复帧耗时并列、99/100/101 帧尾部取样数量变化、恰好 70% 不标记、
选中帧耗时总和相差 1 微秒时落在阈值两侧、空区间与非法输入。
