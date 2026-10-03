package frameperf

import (
	"math/rand"
	"runtime"
	"sort"
	"syscall"
	"testing"
	"time"
)

// peakRSS 返回进程峰值常驻内存（字节）。
func peakRSS() uint64 {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	if runtime.GOOS == "darwin" {
		return uint64(ru.Maxrss) // macOS 单位为字节
	}
	return uint64(ru.Maxrss) * 1024 // Linux 单位为 KiB
}

// TestScale 为可重复执行的规模测试：
// 1,000,000 帧导入，20,000 次不同区间查询（至少一半区间不少于 200,000 帧），
// 报告导入耗时、查询总耗时与峰值内存，并对部分区间核对直接计算结果。
func TestScale(t *testing.T) {
	const (
		numFrames  = 1_000_000
		numQueries = 20_000
	)
	rng := rand.New(rand.NewSource(7))

	frames := make([]Frame, numFrames)
	var ts int64
	for i := range frames {
		ts += int64(rng.Intn(40)) // 密集重复完成时间
		var d int64
		switch rng.Intn(4) {
		case 0:
			d = int64(1 + rng.Intn(10_000_000)) // 大量不同帧耗时
		case 1:
			d = 16_600 // 密集重复值
		case 2:
			d = int64(1 + rng.Intn(1000))
		default:
			d = int64(1 + rng.Intn(10_000_000))
		}
		frames[i] = Frame{CompletionTime: ts, Duration: d}
	}
	total := ts

	// 生成区间：一半大区间（>=200,000 帧），一半随机跨度
	type interval struct{ start, end int64 }
	intervals := make([]interval, numQueries)
	for i := 0; i < numQueries/2; i++ {
		// 大区间：直接按帧下标选取，保证覆盖 >=200,000 帧
		l := rng.Intn(numFrames - 200_000)
		r := l + 200_000 + rng.Intn(numFrames-l-200_000+1)
		intervals[i] = interval{frames[l].CompletionTime, frames[r-1].CompletionTime + 1}
	}
	for i := numQueries / 2; i < numQueries; i++ {
		a := rng.Int63n(total)
		b := rng.Int63n(total)
		if a > b {
			a, b = b, a
		}
		intervals[i] = interval{a, b}
	}

	startImport := time.Now()
	s, err := NewSample(frames, TargetFrameTime60FPS)
	if err != nil {
		t.Fatalf("NewSample: %v", err)
	}
	importDur := time.Since(startImport)

	// 核对部分区间的直接计算结果（取耗时最大的 k 帧求和）
	durations := make([]int64, numFrames)
	completion := make([]int64, numFrames)
	for i, f := range frames {
		durations[i] = f.Duration
		completion[i] = f.CompletionTime
	}
	checkIdx := []int{0, numQueries / 4, numQueries / 2, numQueries * 3 / 4, numQueries - 1}
	for _, qi := range checkIdx {
		iv := intervals[qi]
		got := s.Query(iv.start, iv.end)
		l := sort.Search(numFrames, func(i int) bool { return completion[i] >= iv.start })
		r := sort.Search(numFrames, func(i int) bool { return completion[i] >= iv.end })
		n := r - l
		if got.Frames != n {
			t.Fatalf("区间 %d 帧数不一致: got %d want %d", qi, got.Frames, n)
		}
		if n == 0 {
			continue
		}
		k := (n + 99) / 100
		sel := make([]int64, n)
		copy(sel, durations[l:r])
		sort.Slice(sel, func(a, b int) bool { return sel[a] > sel[b] })
		var sum int64
		for i := 0; i < k; i++ {
			sum += sel[i]
		}
		if got.Sampled != k || got.DurationSum != sum {
			t.Fatalf("区间 %d 结果不一致: got k=%d sum=%d, want k=%d sum=%d",
				qi, got.Sampled, got.DurationSum, k, sum)
		}
	}

	startQuery := time.Now()
	var flagged, big, bigOK int
	for _, iv := range intervals {
		res := s.Query(iv.start, iv.end)
		if res.Flagged {
			flagged++
		}
	}
	queryDur := time.Since(startQuery)

	// 统计大区间数量（>=200,000 帧）
	for _, iv := range intervals[:numQueries/2] {
		big++
		l := sort.Search(numFrames, func(i int) bool { return completion[i] >= iv.start })
		r := sort.Search(numFrames, func(i int) bool { return completion[i] >= iv.end })
		if r-l >= 200_000 {
			bigOK++
		}
	}

	runtime.GC()
	rss := peakRSS()
	t.Logf("导入 %d 帧耗时: %v (限制 20s)", numFrames, importDur)
	t.Logf("%d 次查询总耗时: %v (限制 5s), 标记区间 %d 个", numQueries, queryDur, flagged)
	t.Logf("大区间(>=200k帧): %d/%d", bigOK, big)
	t.Logf("进程峰值常驻内存: %.1f MiB (限制 512 MiB)", float64(rss)/1024/1024)

	if importDur > 20*time.Second {
		t.Fatalf("导入超时: %v", importDur)
	}
	if queryDur > 5*time.Second {
		t.Fatalf("查询超时: %v", queryDur)
	}
	if bigOK < big {
		t.Fatalf("大区间不足: %d/%d", bigOK, big)
	}
	if rss > 512*1024*1024 {
		t.Fatalf("峰值内存超限: %d 字节", rss)
	}
}
