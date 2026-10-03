package frameperf

import (
	"math/rand"
	"sort"
	"testing"
)

// bruteForce 为独立直接计算的参照实现。
func bruteForce(frames []Frame, targetFrameUs int64, start, end int64) QueryResult {
	res := QueryResult{TargetFPS: float64(1_000_000) / float64(targetFrameUs)}
	if end <= start {
		return res
	}
	var ds []int64
	for _, f := range frames {
		if f.CompletionTime >= start && f.CompletionTime < end {
			ds = append(ds, f.Duration)
		}
	}
	n := len(ds)
	if n == 0 {
		return res
	}
	sort.Slice(ds, func(a, b int) bool { return ds[a] > ds[b] })
	k := (n + 99) / 100
	var sum int64
	for i := 0; i < k; i++ {
		sum += ds[i]
	}
	res.Frames = n
	res.Sampled = k
	res.DurationSum = sum
	res.LowFPS = float64(1_000_000) * float64(k) / float64(sum)
	res.HasData = true
	res.Flagged = 10*int64(k)*targetFrameUs < 7*sum
	return res
}

func checkQuery(t *testing.T, s *Sample, frames []Frame, target, start, end int64) {
	t.Helper()
	got := s.Query(start, end)
	want := bruteForce(frames, target, start, end)
	if got != want {
		t.Fatalf("区间 [%d,%d) 不一致:\n got=%+v\nwant=%+v", start, end, got, want)
	}
}

// 随机小规模采样 + 任意区间，与直接计算逐一比对。
func TestRandomSmallAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		n := 1 + rng.Intn(300)
		frames := make([]Frame, n)
		var ts int64
		for i := range frames {
			ts += int64(rng.Intn(5)) // 允许大量重复完成时间
			d := int64(1 + rng.Intn(100))
			if rng.Intn(3) == 0 {
				d = int64(1 + rng.Intn(10_000_000))
			}
			frames[i] = Frame{CompletionTime: ts, Duration: d}
		}
		target := TargetFrameTime60FPS
		if trial%2 == 1 {
			target = TargetFrameTime30FPS
		}
		s, err := NewSample(frames, target)
		if err != nil {
			t.Fatalf("NewSample: %v", err)
		}
		for q := 0; q < 50; q++ {
			a := ts - 10 + int64(rng.Intn(int(ts+21)))
			b := ts - 10 + int64(rng.Intn(int(ts+21)))
			if a > b {
				a, b = b, a
			}
			checkQuery(t, s, frames, target, a, b)
		}
		// 起止边界：恰好等于首末帧完成时间
		checkQuery(t, s, frames, target, frames[0].CompletionTime, frames[n-1].CompletionTime)
		checkQuery(t, s, frames, target, frames[0].CompletionTime, frames[n-1].CompletionTime+1)
		checkQuery(t, s, frames, target, frames[0].CompletionTime+1, frames[n-1].CompletionTime)
		// 空区间
		checkQuery(t, s, frames, target, 10, 10)
		checkQuery(t, s, frames, target, 20, 10)
		checkQuery(t, s, frames, target, ts+1000, ts+2000)
	}
}

// 重复完成时间的记录仍是不同帧，且仅按完成时间判定区间归属。
func TestDuplicateCompletionTimes(t *testing.T) {
	frames := []Frame{
		{CompletionTime: 100, Duration: 5},
		{CompletionTime: 100, Duration: 50},
		{CompletionTime: 100, Duration: 500},
		{CompletionTime: 200, Duration: 7},
	}
	s, err := NewSample(frames, TargetFrameTime60FPS)
	if err != nil {
		t.Fatalf("NewSample: %v", err)
	}
	// [100,101) 只含前 3 帧；k=1，取最大耗时 500
	got := s.Query(100, 101)
	if got.Frames != 3 || got.Sampled != 1 || got.DurationSum != 500 {
		t.Fatalf("重复完成时间结果错误: %+v", got)
	}
	// 区间含起点不含终点：[100,200) 不含完成时间 200 的帧
	got = s.Query(100, 200)
	if got.Frames != 3 {
		t.Fatalf("终点应排除: %+v", got)
	}
	got = s.Query(100, 201)
	if got.Frames != 4 {
		t.Fatalf("应包含完成时间 200 的帧: %+v", got)
	}
}

// 重复帧耗时并列时仍只取 k 帧。
func TestDuplicateDurationsTie(t *testing.T) {
	frames := make([]Frame, 100)
	for i := range frames {
		frames[i] = Frame{CompletionTime: int64(i + 1), Duration: 9000}
	}
	frames[0].Duration = 1
	s, err := NewSample(frames, TargetFrameTime60FPS)
	if err != nil {
		t.Fatalf("NewSample: %v", err)
	}
	got := s.Query(0, 1000)
	// n=100, k=1，并列的 99 个 9000 中只取 1 个
	if got.Sampled != 1 || got.DurationSum != 9000 {
		t.Fatalf("并列取样错误: %+v", got)
	}
}

// 99 / 100 / 101 帧时尾部取样数量 k 的变化。
func TestTailSampleCountBoundaries(t *testing.T) {
	mk := func(n int) *Sample {
		frames := make([]Frame, n)
		for i := range frames {
			frames[i] = Frame{CompletionTime: int64(i + 1), Duration: int64(100 + i)}
		}
		s, err := NewSample(frames, TargetFrameTime60FPS)
		if err != nil {
			t.Fatalf("NewSample: %v", err)
		}
		return s
	}
	cases := []struct {
		n, wantK int
	}{
		{1, 1}, {99, 1}, {100, 1}, {101, 2}, {200, 2}, {201, 3},
	}
	for _, c := range cases {
		got := mk(c.n).Query(0, 1<<60)
		if got.Frames != c.n || got.Sampled != c.wantK {
			t.Fatalf("n=%d: got k=%d, want k=%d", c.n, got.Sampled, c.wantK)
		}
	}
}

// 恰好等于 70% 不标记；选中帧耗时总和相差 1 微秒时落在阈值两侧。
func TestThresholdExact70Percent(t *testing.T) {
	// k=7（n=700），目标帧耗时 16600：10*7*16600 = 1_162_000 = 7*166_000
	// 选中帧耗时总和恰为 166_000 时，lowFPS 恰为目标 FPS 的 70%。
	build := func(topSum int64) *Sample {
		frames := make([]Frame, 700)
		// 7 个大耗时帧：6 个 23714，最后一个凑足 topSum
		durs := []int64{23714, 23714, 23714, 23714, 23714, 23714, topSum - 6*23714}
		for i := range frames {
			d := int64(1)
			if i < len(durs) {
				d = durs[i]
			}
			frames[i] = Frame{CompletionTime: int64(i + 1), Duration: d}
		}
		s, err := NewSample(frames, TargetFrameTime60FPS)
		if err != nil {
			t.Fatalf("NewSample: %v", err)
		}
		return s
	}
	// 恰好 70%：不标记
	if got := build(166_000).Query(0, 1<<60); got.Flagged {
		t.Fatalf("恰好 70%% 不应标记: %+v", got)
	}
	// 总和多 1 微秒（FPS 更低）：标记
	if got := build(166_001).Query(0, 1<<60); !got.Flagged {
		t.Fatalf("总和 166001 应标记: %+v", got)
	}
	// 总和少 1 微秒（FPS 更高）：不标记
	if got := build(165_999).Query(0, 1<<60); got.Flagged {
		t.Fatalf("总和 165999 不应标记: %+v", got)
	}
}

// 判定必须基于未舍入数值：构造展示精度无法区分、但精确判定在阈值两侧的情形。
func TestThresholdNotAffectedByRounding(t *testing.T) {
	s1 := func() *Sample {
		frames := make([]Frame, 700)
		for i := range frames {
			frames[i] = Frame{CompletionTime: int64(i + 1), Duration: 1}
		}
		// 总和 166_000 -> 恰 70%
		for i, d := range []int64{23714, 23714, 23714, 23714, 23714, 23714, 23716} {
			frames[i].Duration = d
		}
		s, _ := NewSample(frames, TargetFrameTime60FPS)
		return s
	}()
	s2 := func() *Sample {
		frames := make([]Frame, 700)
		for i := range frames {
			frames[i] = Frame{CompletionTime: int64(i + 1), Duration: 1}
		}
		// 总和 166_001 -> 略低于 70%
		for i, d := range []int64{23714, 23714, 23714, 23714, 23714, 23714, 23717} {
			frames[i].Duration = d
		}
		s, _ := NewSample(frames, TargetFrameTime60FPS)
		return s
	}()
	r1 := s1.Query(0, 1<<60)
	r2 := s2.Query(0, 1<<60)
	if r1.Flagged == r2.Flagged {
		t.Fatalf("舍入不应影响判定方向: r1=%+v r2=%+v", r1, r2)
	}
	// 两者展示值在常用精度下相同
	if got1, got2 := float64(int(r1.LowFPS*100))/100, float64(int(r2.LowFPS*100))/100; got1 != got2 {
		t.Fatalf("测试前提失败：展示值应相同: %v vs %v", got1, got2)
	}
}

// 空区间与非法输入。
func TestEmptyAndInvalid(t *testing.T) {
	s, err := NewSample(nil, TargetFrameTime60FPS)
	if err != nil {
		t.Fatalf("NewSample(empty): %v", err)
	}
	if got := s.Query(0, 1<<60); got.HasData || got.Flagged {
		t.Fatalf("空采样不应有指标或标记: %+v", got)
	}
	if _, err := NewSample([]Frame{{CompletionTime: 1, Duration: 10}}, 12345); err == nil {
		t.Fatal("非法目标帧耗时应报错")
	}
	if _, err := NewSample([]Frame{{CompletionTime: 2, Duration: 10}, {CompletionTime: 1, Duration: 10}}, TargetFrameTime60FPS); err == nil {
		t.Fatal("非递减校验应报错")
	}
	if _, err := NewSample([]Frame{{CompletionTime: 1, Duration: 0}}, TargetFrameTime60FPS); err == nil {
		t.Fatal("耗时越界应报错")
	}
	if _, err := NewSample([]Frame{{CompletionTime: 1, Duration: 10_000_001}}, TargetFrameTime60FPS); err == nil {
		t.Fatal("耗时越界应报错")
	}
	// 边界合法值
	if _, err := NewSample([]Frame{{CompletionTime: 1, Duration: 1}, {CompletionTime: 1, Duration: 10_000_000}}, TargetFrameTime30FPS); err != nil {
		t.Fatalf("边界合法值不应报错: %v", err)
	}
}
