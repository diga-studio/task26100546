package perfana

import (
	"math/rand"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// peakRSS 返回进程峰值常驻内存（字节）。
func peakRSS() int64 {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	if runtime.GOOS == "darwin" {
		return ru.Maxrss // darwin 上单位为字节
	}
	return ru.Maxrss * 1024 // linux 上单位为 KiB
}

// TestScale 规模测试：1,000,000 帧导入 + 20,000 次区间查询。
// 报告导入耗时、查询总耗时与峰值内存，并对部分区间核对直接计算结果。
// 用 go test -run TestScale -v 可重复执行；-short 模式下跳过。
func TestScale(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过规模测试")
	}
	const totalFrames = 1_000_000
	const totalQueries = 20_000

	// ---- 生成测试数据（不计时）----
	rng := rand.New(rand.NewSource(7))
	frames := make([]Frame, totalFrames)
	ts := make([]int64, totalFrames)
	for i := range ts {
		// 平均帧间隔约 16ms，叠加抖动，并制造重复完成时间
		ts[i] = int64(i)*16_000 + int64(rng.Intn(2000))
		if i > 0 && rng.Intn(20) == 0 {
			ts[i] = ts[i-1]
		}
	}
	for i := range frames {
		var d int64
		switch rng.Intn(10) {
		case 0, 1:
			d = 16_000 + int64(rng.Intn(100)) // 密集重复值
		default:
			d = 1 + rng.Int63n(10_000_000) // 大量不同帧耗时
		}
		frames[i] = Frame{CompletionTime: ts[i], Duration: d}
	}
	maxTime := ts[totalFrames-1]

	// 生成 20,000 个区间，至少一半包含 >= 200,000 帧
	type interval struct{ start, end int64 }
	queries := make([]interval, totalQueries)
	spanLarge := int64(200_000) * 16_000 // 约 200k 帧的跨度
	for i := range queries {
		var span int64
		if i%2 == 0 {
			span = spanLarge + int64(rng.Intn(int(spanLarge)))
		} else {
			span = 1 + int64(rng.Intn(1_000_000))
		}
		start := int64(rng.Int63n(maxTime - span + 1))
		queries[i] = interval{start, start + span}
	}

	// ---- 导入（计时）----
	runtime.GC()
	importStart := time.Now()
	a, err := Import(frames, TargetFrameTime60FPS)
	if err != nil {
		t.Fatalf("Import 失败: %v", err)
	}
	importDur := time.Since(importStart)

	// ---- 查询（计时）----
	queryStart := time.Now()
	flagged := 0
	for _, q := range queries {
		if a.Query(q.start, q.end).Flagged {
			flagged++
		}
	}
	queryDur := time.Since(queryStart)

	mem := peakRSS()
	t.Logf("导入耗时: %v (上限 20s)", importDur)
	t.Logf("查询总耗时: %v (%d 次, 上限 5s)", queryDur, totalQueries)
	t.Logf("峰值常驻内存: %.1f MiB (上限 512 MiB)", float64(mem)/1024/1024)
	t.Logf("标记区间数: %d / %d", flagged, totalQueries)

	if importDur > 20*time.Second {
		t.Errorf("导入耗时超限: %v", importDur)
	}
	if queryDur > 5*time.Second {
		t.Errorf("查询总耗时超限: %v", queryDur)
	}
	if mem > 512*1024*1024 {
		t.Errorf("峰值内存超限: %d 字节", mem)
	}

	// ---- 抽查部分区间，与直接计算核对 ----
	for i := 0; i < totalQueries; i += 977 {
		q := queries[i]
		got := a.Query(q.start, q.end)
		want := bruteForce(frames, TargetFrameTime60FPS, q.start, q.end)
		checkEqual(t, got, want, "规模测试抽查")
	}
}
