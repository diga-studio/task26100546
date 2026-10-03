package frameperf

import "math/bits"

// waveletTree 在按时间排列的帧耗时序列上建立小波树，
// 支持任意下标区间 [l, r) 内“最大的 k 个值之和”的 O(log σ) 查询。
type waveletTree struct {
	levels []waveletLevel
	values []int64 // 压缩下标对应的原始值（升序）
}

// waveletLevel 为小波树的一层。
type waveletLevel struct {
	zeros   int      // 该层 bit 为 0 的元素个数
	bits    []uint64 // 该层每个元素的 bit 位图
	rank    []uint32 // 每 512 bit 的前缀 1 计数（块首不含本块）
	oneSums []uint64 // oneSums[i] 为前 i 个元素中 bit=1 元素的值之和
}

const rankBlockBits = 512

// buildWavelet 对压缩后的取值下标序列建立小波树；values 为下标对应的原始值（升序）。
func buildWavelet(indexes []int, values []int64) *waveletTree {
	n := len(indexes)
	t := &waveletTree{}
	if n == 0 {
		return t
	}
	t.values = values
	maxIdx := 0
	for _, v := range indexes {
		if v > maxIdx {
			maxIdx = v
		}
	}
	depth := bits.Len(uint(maxIdx)) // 至少为 1（maxIdx=0 时 depth=1）
	if depth == 0 {
		depth = 1
	}
	t.levels = make([]waveletLevel, depth)

	cur := make([]int, n)
	copy(cur, indexes)
	next := make([]int, n)

	words := (n + 63) / 64
	for lev := 0; lev < depth; lev++ {
		shift := uint(depth - 1 - lev)
		lvl := waveletLevel{
			bits:    make([]uint64, words),
			rank:    make([]uint32, (n+rankBlockBits-1)/rankBlockBits+1),
			oneSums: make([]uint64, n+1),
		}
		zeroCount := 0
		oneCount := 0
		var runSum uint64
		for i, v := range cur {
			bit := (v >> shift) & 1
			if bit == 1 {
				lvl.bits[i>>6] |= 1 << (uint(i) & 63)
				runSum += uint64(values[v])
			}
			lvl.oneSums[i+1] = runSum
			if bit == 0 {
				zeroCount++
			} else {
				oneCount++
			}
		}
		lvl.zeros = zeroCount
		// 稳定划分：0 在前，1 在后
		zi, oi := 0, zeroCount
		for _, v := range cur {
			if (v>>shift)&1 == 0 {
				next[zi] = v
				zi++
			} else {
				next[oi] = v
				oi++
			}
		}
		cur, next = next, cur
		// rank 目录：每 rankBlockBits 位记录块首前缀 1 计数
		var acc uint32
		for b := 0; b*rankBlockBits < n; b++ {
			lvl.rank[b] = acc
			start := b * rankBlockBits / 64
			end := (b + 1) * rankBlockBits / 64
			if end > words {
				end = words
			}
			for w := start; w < end; w++ {
				acc += uint32(bits.OnesCount64(lvl.bits[w]))
			}
		}
		t.levels[lev] = lvl
	}
	return t
}

// rank1 返回该层前 i 个元素中 bit=1 的个数。
func (lvl *waveletLevel) rank1(i int) int {
	if i <= 0 {
		return 0
	}
	blk := i / rankBlockBits
	cnt := int(lvl.rank[blk])
	start := blk * rankBlockBits / 64
	end := i / 64
	for w := start; w < end; w++ {
		cnt += bits.OnesCount64(lvl.bits[w])
	}
	if rem := uint(i) & 63; rem != 0 {
		cnt += bits.OnesCount64(lvl.bits[i/64] & ((1 << rem) - 1))
	}
	return cnt
}

// sumTopK 返回下标区间 [l, r) 内最大的 k 个值之和；要求 0 <= k <= r-l。
func (t *waveletTree) sumTopK(l, r, k int) int64 {
	var sum uint64
	val := 0
	for lev := 0; lev < len(t.levels) && k > 0 && l < r; lev++ {
		lvl := &t.levels[lev]
		lOnes := lvl.rank1(l)
		rOnes := lvl.rank1(r)
		ones := rOnes - lOnes
		if ones >= k {
			// k 个最大值全部落在 1 子树
			val |= 1 << uint(len(t.levels)-1-lev)
			l = lvl.zeros + lOnes
			r = lvl.zeros + rOnes
		} else {
			// 取走该区间全部 1 子树元素，再在 0 子树中找剩余
			sum += lvl.oneSums[r] - lvl.oneSums[l]
			k -= ones
			l = l - lOnes
			r = r - rOnes
		}
	}
	if k > 0 && l < r {
		// 叶子中剩余的 k 个元素取值相同
		sum += uint64(k) * uint64(t.values[val])
	}
	return int64(sum)
}
