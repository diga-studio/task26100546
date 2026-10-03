package frameperf

import (
	"fmt"
	"sort"
)

// Frame 表示一帧的采样记录。
type Frame struct {
	// CompletionTime 为帧完成时间，整数微秒，导入时按非递减排列。
	CompletionTime int64
	// Duration 为帧耗时，整数微秒，范围 [1, 10_000_000]。
	Duration int64
}

// 支持的目标帧耗时（微秒）。
const (
	TargetFrameTime60FPS int64 = 16_600
	TargetFrameTime30FPS int64 = 33_300
)

// maxDuration 为帧耗时允许的最大值（微秒）。
const maxDuration int64 = 10_000_000

// Sample 表示一份已导入、可反复查询的帧采样。
type Sample struct {
	completion    []int64 // 各帧完成时间，非递减
	targetFrameUs int64
	tree          *waveletTree
}

// QueryResult 为一次区间查询的独立结果。
type QueryResult struct {
	// Frames 为区间内帧数 n。
	Frames int
	// Sampled 为参与统计的尾部帧数 k = ceil(n/100)。
	Sampled int
	// DurationSum 为耗时最大的 k 帧的耗时总和（微秒）。
	DurationSum int64
	// LowFPS 为 1% low FPS，仅供展示；判定不使用该舍入值。
	LowFPS float64
	// TargetFPS 为目标 FPS，仅供展示。
	TargetFPS float64
	// HasData 表示区间非空；空区间没有低帧率指标。
	HasData bool
	// Flagged 表示 1% low FPS 严格低于目标 FPS 的 70%。
	Flagged bool
}

// NewSample 导入一份完整采样并构建分析结构。
// frames 必须按 CompletionTime 非递减排列；targetFrameUs 只能为 16_600 或 33_300。
// 导入后采样在内部复制保存，调用方可释放或修改原切片。
func NewSample(frames []Frame, targetFrameUs int64) (*Sample, error) {
	if targetFrameUs != TargetFrameTime60FPS && targetFrameUs != TargetFrameTime30FPS {
		return nil, fmt.Errorf("frameperf: 不支持的目标帧耗时 %d，仅支持 %d 或 %d 微秒",
			targetFrameUs, TargetFrameTime60FPS, TargetFrameTime30FPS)
	}
	n := len(frames)
	s := &Sample{
		completion:    make([]int64, n),
		targetFrameUs: targetFrameUs,
	}
	durations := make([]int64, n)
	for i, f := range frames {
		if i > 0 && f.CompletionTime < frames[i-1].CompletionTime {
			return nil, fmt.Errorf("frameperf: 第 %d 帧完成时间 %d 早于前一帧 %d，采样必须按完成时间非递减排列",
				i, f.CompletionTime, frames[i-1].CompletionTime)
		}
		if f.Duration < 1 || f.Duration > maxDuration {
			return nil, fmt.Errorf("frameperf: 第 %d 帧耗时 %d 超出范围 [1, %d] 微秒", i, f.Duration, maxDuration)
		}
		s.completion[i] = f.CompletionTime
		durations[i] = f.Duration
	}
	if n == 0 {
		s.tree = buildWavelet(nil, nil)
		return s, nil
	}
	// 坐标压缩帧耗时，降低小波树层数与内存。
	uniq := make([]int64, n)
	copy(uniq, durations)
	sort.Slice(uniq, func(a, b int) bool { return uniq[a] < uniq[b] })
	m := 1
	for i := 1; i < n; i++ {
		if uniq[i] != uniq[m-1] {
			uniq[m] = uniq[i]
			m++
		}
	}
	uniq = uniq[:m]
	indexes := make([]int, n)
	for i, d := range durations {
		indexes[i] = sort.Search(m, func(j int) bool { return uniq[j] >= d })
	}
	s.tree = buildWavelet(indexes, uniq)
	return s, nil
}

// Query 查询区间 [start, end)（按帧完成时间判定，含起点不含终点）。
// 完成时间落入区间的帧计入其完整耗时，不做裁剪。空区间返回 HasData=false。
func (s *Sample) Query(start, end int64) QueryResult {
	res := QueryResult{
		TargetFPS: float64(1_000_000) / float64(s.targetFrameUs),
	}
	if end <= start {
		return res
	}
	l := sort.Search(len(s.completion), func(i int) bool { return s.completion[i] >= start })
	r := sort.Search(len(s.completion), func(i int) bool { return s.completion[i] >= end })
	n := r - l
	if n <= 0 {
		return res
	}
	k := (n + 99) / 100 // ceil(n/100)
	sum := s.tree.sumTopK(l, r, k)
	res.Frames = n
	res.Sampled = k
	res.DurationSum = sum
	res.LowFPS = float64(1_000_000) * float64(k) / float64(sum)
	res.HasData = true
	// 精确判定：lowFPS < 0.7 * targetFPS
	//   1_000_000*k/sum < (7/10)*(1_000_000/target)
	//   <=> 10*k*target < 7*sum（整数运算，不受展示精度影响）
	res.Flagged = 10*int64(k)*s.targetFrameUs < 7*sum
	return res
}
