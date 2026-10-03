package perfana

import (
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

// bruteForce 独立直接计算区间结果，用于对照。
func bruteForce(frames []Frame, targetFrameTime, start, end int64) QueryResult {
	var ds []int64
	for _, f := range frames {
		if f.CompletionTime >= start && f.CompletionTime < end {
			ds = append(ds, f.Duration)
		}
	}
	n := len(ds)
	if n == 0 {
		return QueryResult{}
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] > ds[j] })
	k := (n + 99) / 100
	var sum int64
	for i := 0; i < k; i++ {
		sum += ds[i]
	}
	// 用有理数精确比较：lowFPS = 1e6*k/sum，阈值 = (7/10)*(1e6/target)
	low := big.NewRat(1_000_000*int64(k), sum)
	threshold := big.NewRat(7_000_000, 10*targetFrameTime)
	return QueryResult{
		Frames:    n,
		K:         k,
		LowFPS:    float64(1_000_000) * float64(k) / float64(sum),
		HasMetric: true,
		Flagged:   low.Cmp(threshold) < 0,
	}
}

func mustImport(t *testing.T, frames []Frame, target int64) *Analyzer {
	t.Helper()
	a, err := Import(frames, target)
	if err != nil {
		t.Fatalf("Import 失败: %v", err)
	}
	return a
}

func checkEqual(t *testing.T, got, want QueryResult, ctx string) {
	t.Helper()
	if got.Frames != want.Frames || got.K != want.K ||
		got.HasMetric != want.HasMetric || got.Flagged != want.Flagged {
		t.Errorf("%s: got %+v, want %+v", ctx, got, want)
	}
	if got.HasMetric && got.LowFPS != want.LowFPS {
		t.Errorf("%s: LowFPS got %v, want %v", ctx, got.LowFPS, want.LowFPS)
	}
}

// TestRandomIntervals 小规模随机采样 + 任意区间，与直接计算对照。
// 覆盖重复完成时间、重复帧耗时、任意重叠区间。
func TestRandomIntervals(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		n := 1 + rng.Intn(400)
		frames := make([]Frame, n)
		ts := make([]int64, n)
		for i := range ts {
			// 小时间域制造大量重复完成时间
			ts[i] = int64(rng.Intn(n/2 + 1))
		}
		sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
		for i := range frames {
			d := int64(1 + rng.Intn(100))
			if rng.Intn(3) == 0 {
				d = 50 // 密集重复耗时
			}
			frames[i] = Frame{CompletionTime: ts[i], Duration: d}
		}
		target := TargetFrameTime60FPS
		if trial%2 == 1 {
			target = TargetFrameTime30FPS
		}
		a := mustImport(t, frames, target)
		for q := 0; q < 200; q++ {
			s := int64(rng.Intn(n + 2))
			e := int64(rng.Intn(n + 2))
			if s > e {
				s, e = e, s
			}
			got := a.Query(s, e)
			want := bruteForce(frames, target, s, e)
			checkEqual(t, got, want, "随机区间")
		}
	}
}

// TestBoundaries 起止边界：含起点、不含终点；完成时间恰好在边界上的帧。
func TestBoundaries(t *testing.T) {
	frames := []Frame{
		{CompletionTime: 100, Duration: 10},
		{CompletionTime: 200, Duration: 20},
		{CompletionTime: 200, Duration: 30}, // 相同完成时间的不同帧
		{CompletionTime: 300, Duration: 40},
	}
	a := mustImport(t, frames, TargetFrameTime60FPS)
	cases := []struct{ start, end int64 }{
		{100, 300}, // 含起点 100，不含终点 300 → 3 帧
		{101, 300}, // 不含 100 → 2 帧
		{100, 301}, // 全部 4 帧
		{200, 201}, // 只含完成时间 200 的两帧
		{0, 100},   // 空
		{300, 300}, // 空
		{301, 400}, // 空
		{400, 100}, // 起点大于终点，空
	}
	for _, c := range cases {
		got := a.Query(c.start, c.end)
		want := bruteForce(frames, TargetFrameTime60FPS, c.start, c.end)
		checkEqual(t, got, want, "边界")
	}
}

// TestTailK 99/100/101 帧时尾部取样数量 k 的变化。
func TestTailK(t *testing.T) {
	build := func(n int) []Frame {
		frames := make([]Frame, n)
		for i := range frames {
			frames[i] = Frame{CompletionTime: int64(i + 1), Duration: int64(i + 1)}
		}
		return frames
	}
	for _, n := range []int{99, 100, 101} {
		frames := build(n)
		a := mustImport(t, frames, TargetFrameTime60FPS)
		got := a.Query(0, int64(n+1))
		wantK := (n + 99) / 100
		if got.K != wantK {
			t.Errorf("n=%d: K got %d, want %d", n, got.K, wantK)
		}
		checkEqual(t, got, bruteForce(frames, TargetFrameTime60FPS, 0, int64(n+1)), "尾部取样")
	}
}

// TestDuplicateDurations 大量重复帧耗时，并列边界只取 k 帧。
func TestDuplicateDurations(t *testing.T) {
	frames := make([]Frame, 200)
	for i := range frames {
		frames[i] = Frame{CompletionTime: int64(i), Duration: 5000}
	}
	frames[0].Duration = 9000
	frames[1].Duration = 9000
	a := mustImport(t, frames, TargetFrameTime60FPS)
	got := a.Query(0, 200)
	want := bruteForce(frames, TargetFrameTime60FPS, 0, 200)
	checkEqual(t, got, want, "重复耗时")
	// k=2，top2 = 9000+9000
	wantLow := float64(1_000_000) * 2 / 18000.0
	if got.LowFPS != wantLow {
		t.Errorf("LowFPS got %v, want %v", got.LowFPS, wantLow)
	}
}

// makeThresholdFrames 构造 n=700（k=7）的采样，使 top-7 耗时总和为指定值。
// 其余 693 帧耗时为 1，不影响 top-7。
func makeThresholdFrames(topSum int64) []Frame {
	base := topSum / 7
	rem := topSum % 7
	frames := make([]Frame, 700)
	for i := range frames {
		frames[i] = Frame{CompletionTime: int64(i + 1), Duration: 1}
	}
	for i := 0; i < 7; i++ {
		d := base
		if int64(i) < rem {
			d++
		}
		frames[i].Duration = d
	}
	return frames
}

// TestThresholdExact70 恰好等于目标 70% 时不标记；
// 选中帧耗时总和相差 1 微秒时落在阈值两侧，标记方向正确。
// 判定式：10*k*T < 7*sum。k=7, T=16600 → 临界 sum = 166000。
func TestThresholdExact70(t *testing.T) {
	cases := []struct {
		topSum  int64
		flagged bool
	}{
		{166000, false}, // 恰好 70%，不标记
		{166001, true},  // 总和多 1µs → lowFPS 略低于 70%，标记
		{165999, false}, // 总和少 1µs → 略高于 70%，不标记
	}
	for _, c := range cases {
		frames := makeThresholdFrames(c.topSum)
		a := mustImport(t, frames, TargetFrameTime60FPS)
		got := a.Query(0, 701)
		if got.Flagged != c.flagged {
			t.Errorf("topSum=%d: Flagged got %v, want %v (LowFPS=%v)",
				c.topSum, got.Flagged, c.flagged, got.LowFPS)
		}
		checkEqual(t, got, bruteForce(frames, TargetFrameTime60FPS, 0, 701), "阈值")
	}
}

// TestThreshold30FPS 30FPS 目标下的阈值边界。
// k=7, T=33300 → 临界 sum = 10*7*33300/7 = 333000。
func TestThreshold30FPS(t *testing.T) {
	cases := []struct {
		topSum  int64
		flagged bool
	}{
		{333000, false},
		{333001, true},
		{332999, false},
	}
	for _, c := range cases {
		frames := makeThresholdFrames(c.topSum)
		a := mustImport(t, frames, TargetFrameTime30FPS)
		got := a.Query(0, 701)
		if got.Flagged != c.flagged {
			t.Errorf("topSum=%d: Flagged got %v, want %v", c.topSum, got.Flagged, c.flagged)
		}
	}
}

// TestEmptyInterval 空区间无指标、不标记。
func TestEmptyInterval(t *testing.T) {
	a := mustImport(t, []Frame{{CompletionTime: 10, Duration: 100}}, TargetFrameTime60FPS)
	got := a.Query(0, 10)
	if got.HasMetric || got.Flagged || got.Frames != 0 || got.K != 0 {
		t.Errorf("空区间结果错误: %+v", got)
	}
	// 空采样
	a2 := mustImport(t, nil, TargetFrameTime30FPS)
	got2 := a2.Query(0, 1_000_000)
	if got2.HasMetric || got2.Flagged {
		t.Errorf("空采样结果错误: %+v", got2)
	}
}

// TestImportValidation 导入参数校验。
func TestImportValidation(t *testing.T) {
	if _, err := Import(nil, 16000); err == nil {
		t.Error("非法目标帧耗时未报错")
	}
	bad := []Frame{{CompletionTime: 5, Duration: 1}, {CompletionTime: 4, Duration: 1}}
	if _, err := Import(bad, TargetFrameTime60FPS); err == nil {
		t.Error("非递减约束未报错")
	}
	if _, err := Import([]Frame{{CompletionTime: 1, Duration: 0}}, TargetFrameTime60FPS); err == nil {
		t.Error("耗时下界未报错")
	}
	if _, err := Import([]Frame{{CompletionTime: 1, Duration: 10_000_001}}, TargetFrameTime60FPS); err == nil {
		t.Error("耗时上界未报错")
	}
	if _, err := Import([]Frame{{CompletionTime: 1, Duration: 10_000_000}}, TargetFrameTime60FPS); err != nil {
		t.Errorf("边界耗时应合法: %v", err)
	}
}
