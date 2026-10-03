package perfana

// mergeTree 是归并排序树：按帧完成时间顺序组织，每个节点保存覆盖区间内
// 已排序的帧耗时及其前缀和，用于在任意时间区间上快速求“耗时最大的 k 帧之和”。
type mergeTree struct {
	sorted [][]int32 // 每个节点覆盖区间内升序排列的帧耗时
	prefix [][]int64 // 与 sorted 对应的前缀和，prefix[i][0]=0
	n      int       // 叶子对应的帧数
}

// buildMergeTree 以 durations（按完成时间顺序）构建归并排序树。
func buildMergeTree(durations []int32) *mergeTree {
	n := len(durations)
	t := &mergeTree{
		sorted: make([][]int32, 2*n),
		prefix: make([][]int64, 2*n),
		n:      n,
	}
	var build func(node, lo, hi int)
	build = func(node, lo, hi int) {
		if hi-lo == 1 {
			t.sorted[node] = []int32{durations[lo]}
			t.prefix[node] = []int64{0, int64(durations[lo])}
			return
		}
		mid := (lo + hi) / 2
		left, right := node+1, node+2*(mid-lo)
		build(left, lo, mid)
		build(right, mid, hi)
		ls, rs := t.sorted[left], t.sorted[right]
		merged := make([]int32, 0, len(ls)+len(rs))
		i, j := 0, 0
		for i < len(ls) && j < len(rs) {
			if ls[i] <= rs[j] {
				merged = append(merged, ls[i])
				i++
			} else {
				merged = append(merged, rs[j])
				j++
			}
		}
		merged = append(merged, ls[i:]...)
		merged = append(merged, rs[j:]...)
		pre := make([]int64, len(merged)+1)
		for k, v := range merged {
			pre[k+1] = pre[k] + int64(v)
		}
		t.sorted[node] = merged
		t.prefix[node] = pre
	}
	if n > 0 {
		build(0, 0, n)
	}
	return t
}

// countGE 返回下标区间 [lo, hi) 内耗时 >= v 的帧数。
func (t *mergeTree) countGE(lo, hi int, v int32) int {
	count, _ := t.query(lo, hi, v)
	return count
}

// sumGE 返回下标区间 [lo, hi) 内耗时 >= v 的帧耗时总和。
func (t *mergeTree) sumGE(lo, hi int, v int32) int64 {
	_, sum := t.query(lo, hi, v)
	return sum
}

// query 同时返回下标区间 [lo, hi) 内耗时 >= v 的帧数与耗时总和。
func (t *mergeTree) query(lo, hi int, v int32) (int, int64) {
	count := 0
	sum := int64(0)
	var walk func(node, nl, nh int)
	walk = func(node, nl, nh int) {
		if hi <= nl || nh <= lo {
			return
		}
		if lo <= nl && nh <= hi {
			s := t.sorted[node]
			// 第一个 >= v 的位置
			idx := sortSearchInts(s, v)
			cnt := len(s) - idx
			if cnt > 0 {
				count += cnt
				sum += t.prefix[node][len(s)] - t.prefix[node][idx]
			}
			return
		}
		mid := (nl + nh) / 2
		walk(node+1, nl, mid)
		walk(node+2*(mid-nl), mid, nh)
	}
	walk(0, 0, t.n)
	return count, sum
}

// sortSearchInts 返回 s（升序）中第一个 >= v 的下标。
func sortSearchInts(s []int32, v int32) int {
	lo, hi := 0, len(s)
	for lo < hi {
		mid := (lo + hi) / 2
		if s[mid] < v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}
