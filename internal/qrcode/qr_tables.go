package qrcode

// QR 码规格常量表（版本 1..40 的纠错块结构），数据来自 ISO/IEC 18004。
// 由 tools 脚本生成，勿手改。

// qrECGroup 是一种块组：count 个块，每块 dataCW 个数据码字。
type qrECGroup struct{ count, dataCW int }

// qrECLevel 是某版本某纠错等级的参数：每块纠错码字数 + 块组。
type qrECLevel struct {
	ecPerBlock int
	groups     []qrECGroup
}

// qrVersion 是一个版本的排布信息。levels 顺序：L、M、Q、H。
type qrVersion struct {
	alignment []int
	levels    [4]qrECLevel
}

// qrVersions 下标 = 版本号（0 占位）。
var qrVersions = [41]qrVersion{
	1: {alignment: []int{}, levels: [4]qrECLevel{
		{ecPerBlock: 7, groups: []qrECGroup{{1, 19}}},
		{ecPerBlock: 10, groups: []qrECGroup{{1, 16}}},
		{ecPerBlock: 13, groups: []qrECGroup{{1, 13}}},
		{ecPerBlock: 17, groups: []qrECGroup{{1, 9}}},
	}},
	2: {alignment: []int{6, 18}, levels: [4]qrECLevel{
		{ecPerBlock: 10, groups: []qrECGroup{{1, 34}}},
		{ecPerBlock: 16, groups: []qrECGroup{{1, 28}}},
		{ecPerBlock: 22, groups: []qrECGroup{{1, 22}}},
		{ecPerBlock: 28, groups: []qrECGroup{{1, 16}}},
	}},
	3: {alignment: []int{6, 22}, levels: [4]qrECLevel{
		{ecPerBlock: 15, groups: []qrECGroup{{1, 55}}},
		{ecPerBlock: 26, groups: []qrECGroup{{1, 44}}},
		{ecPerBlock: 18, groups: []qrECGroup{{2, 17}}},
		{ecPerBlock: 22, groups: []qrECGroup{{2, 13}}},
	}},
	4: {alignment: []int{6, 26}, levels: [4]qrECLevel{
		{ecPerBlock: 20, groups: []qrECGroup{{1, 80}}},
		{ecPerBlock: 18, groups: []qrECGroup{{2, 32}}},
		{ecPerBlock: 26, groups: []qrECGroup{{2, 24}}},
		{ecPerBlock: 16, groups: []qrECGroup{{4, 9}}},
	}},
	5: {alignment: []int{6, 30}, levels: [4]qrECLevel{
		{ecPerBlock: 26, groups: []qrECGroup{{1, 108}}},
		{ecPerBlock: 24, groups: []qrECGroup{{2, 43}}},
		{ecPerBlock: 18, groups: []qrECGroup{{2, 15}, {2, 16}}},
		{ecPerBlock: 22, groups: []qrECGroup{{2, 11}, {2, 12}}},
	}},
	6: {alignment: []int{6, 34}, levels: [4]qrECLevel{
		{ecPerBlock: 18, groups: []qrECGroup{{2, 68}}},
		{ecPerBlock: 16, groups: []qrECGroup{{4, 27}}},
		{ecPerBlock: 24, groups: []qrECGroup{{4, 19}}},
		{ecPerBlock: 28, groups: []qrECGroup{{4, 15}}},
	}},
	7: {alignment: []int{6, 22, 38}, levels: [4]qrECLevel{
		{ecPerBlock: 20, groups: []qrECGroup{{2, 78}}},
		{ecPerBlock: 18, groups: []qrECGroup{{4, 31}}},
		{ecPerBlock: 18, groups: []qrECGroup{{2, 14}, {4, 15}}},
		{ecPerBlock: 26, groups: []qrECGroup{{4, 13}, {1, 14}}},
	}},
	8: {alignment: []int{6, 24, 42}, levels: [4]qrECLevel{
		{ecPerBlock: 24, groups: []qrECGroup{{2, 97}}},
		{ecPerBlock: 22, groups: []qrECGroup{{2, 38}, {2, 39}}},
		{ecPerBlock: 22, groups: []qrECGroup{{4, 18}, {2, 19}}},
		{ecPerBlock: 26, groups: []qrECGroup{{4, 14}, {2, 15}}},
	}},
	9: {alignment: []int{6, 26, 46}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{2, 116}}},
		{ecPerBlock: 22, groups: []qrECGroup{{3, 36}, {2, 37}}},
		{ecPerBlock: 20, groups: []qrECGroup{{4, 16}, {4, 17}}},
		{ecPerBlock: 24, groups: []qrECGroup{{4, 12}, {4, 13}}},
	}},
	10: {alignment: []int{6, 28, 50}, levels: [4]qrECLevel{
		{ecPerBlock: 18, groups: []qrECGroup{{2, 68}, {2, 69}}},
		{ecPerBlock: 26, groups: []qrECGroup{{4, 43}, {1, 44}}},
		{ecPerBlock: 24, groups: []qrECGroup{{6, 19}, {2, 20}}},
		{ecPerBlock: 28, groups: []qrECGroup{{6, 15}, {2, 16}}},
	}},
	11: {alignment: []int{6, 30, 54}, levels: [4]qrECLevel{
		{ecPerBlock: 20, groups: []qrECGroup{{4, 81}}},
		{ecPerBlock: 30, groups: []qrECGroup{{1, 50}, {4, 51}}},
		{ecPerBlock: 28, groups: []qrECGroup{{4, 22}, {4, 23}}},
		{ecPerBlock: 24, groups: []qrECGroup{{3, 12}, {8, 13}}},
	}},
	12: {alignment: []int{6, 32, 58}, levels: [4]qrECLevel{
		{ecPerBlock: 24, groups: []qrECGroup{{2, 92}, {2, 93}}},
		{ecPerBlock: 22, groups: []qrECGroup{{6, 36}, {2, 37}}},
		{ecPerBlock: 26, groups: []qrECGroup{{4, 20}, {6, 21}}},
		{ecPerBlock: 28, groups: []qrECGroup{{7, 14}, {4, 15}}},
	}},
	13: {alignment: []int{6, 34, 62}, levels: [4]qrECLevel{
		{ecPerBlock: 26, groups: []qrECGroup{{4, 107}}},
		{ecPerBlock: 22, groups: []qrECGroup{{8, 37}, {1, 38}}},
		{ecPerBlock: 24, groups: []qrECGroup{{8, 20}, {4, 21}}},
		{ecPerBlock: 22, groups: []qrECGroup{{12, 11}, {4, 12}}},
	}},
	14: {alignment: []int{6, 26, 46, 66}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{3, 115}, {1, 116}}},
		{ecPerBlock: 24, groups: []qrECGroup{{4, 40}, {5, 41}}},
		{ecPerBlock: 20, groups: []qrECGroup{{11, 16}, {5, 17}}},
		{ecPerBlock: 24, groups: []qrECGroup{{11, 12}, {5, 13}}},
	}},
	15: {alignment: []int{6, 26, 48, 70}, levels: [4]qrECLevel{
		{ecPerBlock: 22, groups: []qrECGroup{{5, 87}, {1, 88}}},
		{ecPerBlock: 24, groups: []qrECGroup{{5, 41}, {5, 42}}},
		{ecPerBlock: 30, groups: []qrECGroup{{5, 24}, {7, 25}}},
		{ecPerBlock: 24, groups: []qrECGroup{{11, 12}, {7, 13}}},
	}},
	16: {alignment: []int{6, 26, 50, 74}, levels: [4]qrECLevel{
		{ecPerBlock: 24, groups: []qrECGroup{{5, 98}, {1, 99}}},
		{ecPerBlock: 28, groups: []qrECGroup{{7, 45}, {3, 46}}},
		{ecPerBlock: 24, groups: []qrECGroup{{15, 19}, {2, 20}}},
		{ecPerBlock: 30, groups: []qrECGroup{{3, 15}, {13, 16}}},
	}},
	17: {alignment: []int{6, 30, 54, 78}, levels: [4]qrECLevel{
		{ecPerBlock: 28, groups: []qrECGroup{{1, 107}, {5, 108}}},
		{ecPerBlock: 28, groups: []qrECGroup{{10, 46}, {1, 47}}},
		{ecPerBlock: 28, groups: []qrECGroup{{1, 22}, {15, 23}}},
		{ecPerBlock: 28, groups: []qrECGroup{{2, 14}, {17, 15}}},
	}},
	18: {alignment: []int{6, 30, 56, 82}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{5, 120}, {1, 121}}},
		{ecPerBlock: 26, groups: []qrECGroup{{9, 43}, {4, 44}}},
		{ecPerBlock: 28, groups: []qrECGroup{{17, 22}, {1, 23}}},
		{ecPerBlock: 28, groups: []qrECGroup{{2, 14}, {19, 15}}},
	}},
	19: {alignment: []int{6, 30, 58, 86}, levels: [4]qrECLevel{
		{ecPerBlock: 28, groups: []qrECGroup{{3, 113}, {4, 114}}},
		{ecPerBlock: 26, groups: []qrECGroup{{3, 44}, {11, 45}}},
		{ecPerBlock: 26, groups: []qrECGroup{{17, 21}, {4, 22}}},
		{ecPerBlock: 26, groups: []qrECGroup{{9, 13}, {16, 14}}},
	}},
	20: {alignment: []int{6, 34, 62, 90}, levels: [4]qrECLevel{
		{ecPerBlock: 28, groups: []qrECGroup{{3, 107}, {5, 108}}},
		{ecPerBlock: 26, groups: []qrECGroup{{3, 41}, {13, 42}}},
		{ecPerBlock: 30, groups: []qrECGroup{{15, 24}, {5, 25}}},
		{ecPerBlock: 28, groups: []qrECGroup{{15, 15}, {10, 16}}},
	}},
	21: {alignment: []int{6, 28, 50, 72, 94}, levels: [4]qrECLevel{
		{ecPerBlock: 28, groups: []qrECGroup{{4, 116}, {4, 117}}},
		{ecPerBlock: 26, groups: []qrECGroup{{17, 42}}},
		{ecPerBlock: 28, groups: []qrECGroup{{17, 22}, {6, 23}}},
		{ecPerBlock: 30, groups: []qrECGroup{{19, 16}, {6, 17}}},
	}},
	22: {alignment: []int{6, 26, 50, 74, 98}, levels: [4]qrECLevel{
		{ecPerBlock: 28, groups: []qrECGroup{{2, 111}, {7, 112}}},
		{ecPerBlock: 28, groups: []qrECGroup{{17, 46}}},
		{ecPerBlock: 30, groups: []qrECGroup{{7, 24}, {16, 25}}},
		{ecPerBlock: 24, groups: []qrECGroup{{34, 13}}},
	}},
	23: {alignment: []int{6, 30, 54, 78, 102}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{4, 121}, {5, 122}}},
		{ecPerBlock: 28, groups: []qrECGroup{{4, 47}, {14, 48}}},
		{ecPerBlock: 30, groups: []qrECGroup{{11, 24}, {14, 25}}},
		{ecPerBlock: 30, groups: []qrECGroup{{16, 15}, {14, 16}}},
	}},
	24: {alignment: []int{6, 28, 54, 80, 106}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{6, 117}, {4, 118}}},
		{ecPerBlock: 28, groups: []qrECGroup{{6, 45}, {14, 46}}},
		{ecPerBlock: 30, groups: []qrECGroup{{11, 24}, {16, 25}}},
		{ecPerBlock: 30, groups: []qrECGroup{{30, 16}, {2, 17}}},
	}},
	25: {alignment: []int{6, 32, 58, 84, 110}, levels: [4]qrECLevel{
		{ecPerBlock: 26, groups: []qrECGroup{{8, 106}, {4, 107}}},
		{ecPerBlock: 28, groups: []qrECGroup{{8, 47}, {13, 48}}},
		{ecPerBlock: 30, groups: []qrECGroup{{7, 24}, {22, 25}}},
		{ecPerBlock: 30, groups: []qrECGroup{{22, 15}, {13, 16}}},
	}},
	26: {alignment: []int{6, 30, 58, 86, 114}, levels: [4]qrECLevel{
		{ecPerBlock: 28, groups: []qrECGroup{{10, 114}, {2, 115}}},
		{ecPerBlock: 28, groups: []qrECGroup{{19, 46}, {4, 47}}},
		{ecPerBlock: 28, groups: []qrECGroup{{28, 22}, {6, 23}}},
		{ecPerBlock: 30, groups: []qrECGroup{{33, 16}, {4, 17}}},
	}},
	27: {alignment: []int{6, 34, 62, 90, 118}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{8, 122}, {4, 123}}},
		{ecPerBlock: 28, groups: []qrECGroup{{22, 45}, {3, 46}}},
		{ecPerBlock: 30, groups: []qrECGroup{{8, 23}, {26, 24}}},
		{ecPerBlock: 30, groups: []qrECGroup{{12, 15}, {28, 16}}},
	}},
	28: {alignment: []int{6, 26, 50, 74, 98, 122}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{3, 117}, {10, 118}}},
		{ecPerBlock: 28, groups: []qrECGroup{{3, 45}, {23, 46}}},
		{ecPerBlock: 30, groups: []qrECGroup{{4, 24}, {31, 25}}},
		{ecPerBlock: 30, groups: []qrECGroup{{11, 15}, {31, 16}}},
	}},
	29: {alignment: []int{6, 30, 54, 78, 102, 126}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{7, 116}, {7, 117}}},
		{ecPerBlock: 28, groups: []qrECGroup{{21, 45}, {7, 46}}},
		{ecPerBlock: 30, groups: []qrECGroup{{1, 23}, {37, 24}}},
		{ecPerBlock: 30, groups: []qrECGroup{{19, 15}, {26, 16}}},
	}},
	30: {alignment: []int{6, 26, 52, 78, 104, 130}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{5, 115}, {10, 116}}},
		{ecPerBlock: 28, groups: []qrECGroup{{19, 47}, {10, 48}}},
		{ecPerBlock: 30, groups: []qrECGroup{{15, 24}, {25, 25}}},
		{ecPerBlock: 30, groups: []qrECGroup{{23, 15}, {25, 16}}},
	}},
	31: {alignment: []int{6, 30, 56, 82, 108, 134}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{13, 115}, {3, 116}}},
		{ecPerBlock: 28, groups: []qrECGroup{{2, 46}, {29, 47}}},
		{ecPerBlock: 30, groups: []qrECGroup{{42, 24}, {1, 25}}},
		{ecPerBlock: 30, groups: []qrECGroup{{23, 15}, {28, 16}}},
	}},
	32: {alignment: []int{6, 34, 60, 86, 112, 138}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{17, 115}}},
		{ecPerBlock: 28, groups: []qrECGroup{{10, 46}, {23, 47}}},
		{ecPerBlock: 30, groups: []qrECGroup{{10, 24}, {35, 25}}},
		{ecPerBlock: 30, groups: []qrECGroup{{19, 15}, {35, 16}}},
	}},
	33: {alignment: []int{6, 30, 58, 86, 114, 142}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{17, 115}, {1, 116}}},
		{ecPerBlock: 28, groups: []qrECGroup{{14, 46}, {21, 47}}},
		{ecPerBlock: 30, groups: []qrECGroup{{29, 24}, {19, 25}}},
		{ecPerBlock: 30, groups: []qrECGroup{{11, 15}, {46, 16}}},
	}},
	34: {alignment: []int{6, 34, 62, 90, 118, 146}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{13, 115}, {6, 116}}},
		{ecPerBlock: 28, groups: []qrECGroup{{14, 46}, {23, 47}}},
		{ecPerBlock: 30, groups: []qrECGroup{{44, 24}, {7, 25}}},
		{ecPerBlock: 30, groups: []qrECGroup{{59, 16}, {1, 17}}},
	}},
	35: {alignment: []int{6, 30, 54, 78, 102, 126, 150}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{12, 121}, {7, 122}}},
		{ecPerBlock: 28, groups: []qrECGroup{{12, 47}, {26, 48}}},
		{ecPerBlock: 30, groups: []qrECGroup{{39, 24}, {14, 25}}},
		{ecPerBlock: 30, groups: []qrECGroup{{22, 15}, {41, 16}}},
	}},
	36: {alignment: []int{6, 24, 50, 76, 102, 128, 154}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{6, 121}, {14, 122}}},
		{ecPerBlock: 28, groups: []qrECGroup{{6, 47}, {34, 48}}},
		{ecPerBlock: 30, groups: []qrECGroup{{46, 24}, {10, 25}}},
		{ecPerBlock: 30, groups: []qrECGroup{{2, 15}, {64, 16}}},
	}},
	37: {alignment: []int{6, 28, 54, 80, 106, 132, 158}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{17, 122}, {4, 123}}},
		{ecPerBlock: 28, groups: []qrECGroup{{29, 46}, {14, 47}}},
		{ecPerBlock: 30, groups: []qrECGroup{{49, 24}, {10, 25}}},
		{ecPerBlock: 30, groups: []qrECGroup{{24, 15}, {46, 16}}},
	}},
	38: {alignment: []int{6, 32, 58, 84, 110, 136, 162}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{4, 122}, {18, 123}}},
		{ecPerBlock: 28, groups: []qrECGroup{{13, 46}, {32, 47}}},
		{ecPerBlock: 30, groups: []qrECGroup{{48, 24}, {14, 25}}},
		{ecPerBlock: 30, groups: []qrECGroup{{42, 15}, {32, 16}}},
	}},
	39: {alignment: []int{6, 26, 54, 82, 110, 138, 166}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{20, 117}, {4, 118}}},
		{ecPerBlock: 28, groups: []qrECGroup{{40, 47}, {7, 48}}},
		{ecPerBlock: 30, groups: []qrECGroup{{43, 24}, {22, 25}}},
		{ecPerBlock: 30, groups: []qrECGroup{{10, 15}, {67, 16}}},
	}},
	40: {alignment: []int{6, 30, 58, 86, 114, 142, 170}, levels: [4]qrECLevel{
		{ecPerBlock: 30, groups: []qrECGroup{{19, 118}, {6, 119}}},
		{ecPerBlock: 28, groups: []qrECGroup{{18, 47}, {31, 48}}},
		{ecPerBlock: 30, groups: []qrECGroup{{34, 24}, {34, 25}}},
		{ecPerBlock: 30, groups: []qrECGroup{{20, 15}, {61, 16}}},
	}},
}
