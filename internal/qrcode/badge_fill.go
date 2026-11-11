package qrcode

import "crypto/sha256"

// badgeFilledGrid 只给绘制用的网格副本补纹理，不改变编码数据或模块排列表。
// 徽标占据第 42~48 行的外圈。向后取 8 行的排列表作为范围模板，可避开
// 定位图案，并保持各版本的行密度。只补模板有、原表没有的格位，
// 第 0~6 列的同步环、固定模块和格式环始终保持原样。
func badgeFilledGrid(info *EncodeInfo, fill bool) [72][16]byte {
	g := info.Grid
	if !fill || info.Version < 0 || info.Version > 2 {
		return g
	}
	// 直接复制附近数据时，零散短点常会集中在内侧，外侧仍显得缺了一块。
	// 这里交错安排长线和短点，让内、中、外圈都有墨量；每条线的
	// 粗细和画法仍由原有渲染器决定。bit 0~7 对应第 7~14 列，按可绘制行
	// 顺序使用，避免低版本跳行后恰好只剩下同一类线段。
	// 每类变体保持相近的墨量与径向重心，URL 的散列只决定类内选择，
	// 因此内容变化不会再次造成外圈偏空。相同内容在 SVG/位图中选取一致，
	// 不依赖时间、进程状态或随机数。
	// 独立点必须与两侧格位断开，且不能放在会被第 15 列延长的最外缘；
	// 中圈与错落图案都保留这样的断点，避免选中的组合全被连成直线。
	patterns := [...][8]byte{
		{0xe4, 0xe2, 0xe5, 0xc9, 0xca, 0xd2, 0xd3, 0xe9}, // 外圈长线 + 内侧点
		{0x35, 0x69, 0x59, 0x65, 0x2d, 0x4d, 0x5a, 0x6b}, // 中圈短线 + 独立点
		{0xad, 0xab, 0xb5, 0xa6, 0xac, 0x95, 0x96, 0xa9}, // 内外错落的点与线
	}
	roles := [...]int{0, 1, 2, 1, 0, 1, 2}
	seed := sha256.Sum256([]byte(info.Payload))
	line := 0
	for row := 42; row <= 48; row++ {
		source := row + 8
		missing := placeTable[info.Version][source] &^ placeTable[info.Version][row]
		missing &= 0x7f80 // 只填第 7~14 列，外缘随后统一延伸
		if missing == 0 {
			continue
		}
		pattern := uint16(patterns[roles[line]][seed[line]&7]) << 7
		line++
		for col := 7; col < 15; col++ {
			if missing&(1<<uint(col)) != 0 {
				g[row][col] = byte((pattern >> uint(col)) & 1)
			}
		}
		// 第 15 列是外缘延伸，始终复制第 14 列，与原生绘制规则一致。
		g[row][15] = g[row][14]
	}
	return g
}
