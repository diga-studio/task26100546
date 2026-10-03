// Package perfana 提供游戏构建性能分析能力：
// 导入一份帧采样后，可对任意时间区间反复查询 1% low FPS
// 及是否低于目标 FPS 的 70%。采样与分析数据全部保存在进程内存中。
package perfana

import (
	"errors"
	"fmt"
	"sort"
)

// Frame 表示一帧的记录。
type Frame struct {
	// CompletionTime 帧完成时间，整数微秒。采样中按该字段非递减排列，
	// 相同完成时间的记录仍是不同帧。
	CompletionTime int64
	// Duration 帧耗时，整数微秒，范围 [1, 10_000_000]。
	Duration int64
}

// 允许的目标帧耗时（微秒）。
const (
	TargetFrameTime60FPS int64 = 16600
	TargetFrameTime30FPS int64 = 33300
)

// maxDuration 帧耗时上限（微秒）。
const maxDuration = 10_000_000

// QueryResult 是一次区间查询的独立结果。
type QueryResult struct {
	// Frames 区间内按完成时间计入的帧数 n。
	Frames int
	// K 参与低帧率统计的帧数，等于 ceil(n/100)；空区间为 0。
	K int
	// LowFPS 1% low FPS = 1_000_000*K / (耗时最大的 K 帧耗时总和)。
	// 空区间无指标，为 0。
	LowFPS float64
	// HasMetric 区间非空时为 true；空区间没有低帧率指标。
	HasMetric bool
	// Flagged LowFPS 严格低于目标 FPS 的 70% 时为 true（依据未舍入数值判定）。
	// 空区间不标记。
	Flagged bool
}

// Analyzer 保存一份已导入采样及其分析数据，可反复执行区间查询。
type Analyzer struct {
	targetFrameTime int64
	times           []int64 // 帧完成时间，非递减
	tree            *mergeTree
}

// Import 导入一份完整采样并构建分析数据。
// frames 必须按 CompletionTime 非递减排列；targetFrameTime 必须为
// TargetFrameTime60FPS 或 TargetFrameTime30FPS，导入后不再修改。
func Import(frames []Frame, targetFrameTime int64) (*Analyzer, error) {
	if targetFrameTime != TargetFrameTime60FPS && targetFrameTime != TargetFrameTime30FPS {
		return nil, fmt.Errorf("perfana: 非法目标帧耗时 %d，仅支持 %d 或 %d 微秒",
			targetFrameTime, TargetFrameTime60FPS, TargetFrameTime30FPS)
	}
	times := make([]int64, len(frames))
	durations := make([]int32, len(frames))
	for i, f := range frames {
		if f.Duration < 1 || f.Duration > maxDuration {
			return nil, fmt.Errorf("perfana: 第 %d 帧耗时 %d 超出范围 [1, %d]", i, f.Duration, maxDuration)
		}
		if i > 0 && f.CompletionTime < frames[i-1].CompletionTime {
			return nil, errors.New("perfana: 帧记录必须按完成时间非递减排列")
		}
		times[i] = f.CompletionTime
		durations[i] = int32(f.Duration)
	}
	return &Analyzer{
		targetFrameTime: targetFrameTime,
		times:           times,
		tree:            buildMergeTree(durations),
	}, nil
}

// Query 查询区间 [start, end)（含起点、不含终点）的低帧率指标。
// 仅按帧完成时间决定是否纳入，帧耗时不做裁剪。
func (a *Analyzer) Query(start, end int64) QueryResult {
	lo := sort.Search(len(a.times), func(i int) bool { return a.times[i] >= start })
	hi := sort.Search(len(a.times), func(i int) bool { return a.times[i] >= end })
	n := hi - lo
	if n <= 0 {
		return QueryResult{}
	}
	k := (n + 99) / 100
	sum := a.topKDurationSum(lo, hi, k)
	res := QueryResult{
		Frames:    n,
		K:         k,
		LowFPS:    float64(1_000_000) * float64(k) / float64(sum),
		HasMetric: true,
	}
	// 判定：LowFPS < 70% * TargetFPS
	//   1e6*k/sum < (7/10)*(1e6/targetFrameTime)
	//   <=> 10*k*targetFrameTime < 7*sum
	// 全程整数运算，不受展示精度影响；等于 70% 时不标记。
	res.Flagged = 10*int64(k)*a.targetFrameTime < 7*sum
	return res
}

// topKDurationSum 返回下标区间 [lo, hi) 内耗时最大的 k 帧的耗时总和。
// 边界耗时并列时只取 k 帧，不扩大数量。
func (a *Analyzer) topKDurationSum(lo, hi, k int) int64 {
	// 二分找到第 k 大耗时 v：满足 countGE(v) >= k 的最大 v。
	low, high := int32(1), int32(maxDuration)
	for low < high {
		mid := low + (high-low+1)/2
		if a.tree.countGE(lo, hi, mid) >= k {
			low = mid
		} else {
			high = mid - 1
		}
	}
	v := low
	// 严格大于 v 的全部计入，并列的 v 只补足到 k 帧。
	countGT, sumGT := a.tree.query(lo, hi, v+1)
	return sumGT + int64(k-countGT)*int64(v)
}
