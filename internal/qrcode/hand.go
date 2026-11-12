package qrcode

import "math"

// 手绘风格的墨线几何（600×600 坐标系，PNG 与 SVG 共用）。
//
// 一个游程不再画成一根等宽胶囊，而是画成「一笔带过」的墨迹：
// 笔宽沿长度起伏（落笔重、提笔轻）、中线跟着摆、两端各带一点出头，
// 落笔的位置和角度也各不相同 —— 像用马克笔在纸上画的。
// 三个定位环同样画成边缘不匀、收笔微微出头的手绘圆环。
//
// 摆幅不是拍脑袋定的：每个点能偏多少由 handRoom 现算（既要盖住自己的格心，
// 又不能蹭到邻行的格心），所以手绘可以很野，码还是扫得出来。
//
// 所有抖动都由 (行,列) 散列出的确定性噪声驱动 —— 同一个码永远画出同一张图，
// 不会因为"随机"导致同一请求两次结果不同。

// inkPt 是手绘笔画上的一个点：画布坐标 + 该点的笔宽（600 坐标系）。
type inkPt struct{ X, Y, W float64 }

// inkPass 是一条独立的墨线笔画（一道墨）。
//
// Erase 为真时这一道不是落墨而是擦白：笔画风用它留出「飞白」——
// 涂过的地方还剩下几丝没吃到墨的白缝。
type inkPass struct {
	Pts   []inkPt
	Erase bool
}

// 手绘抖动幅度（600 坐标系下的像素）。格子边长约 11.1、笔宽约 10.8。
//
// 这些是「夸张度」的上限，真正每个点抖多少由 handRoom 现算 —— 手绘可以很野，
// 但格心必须落进墨迹里，否则码就扫不出来了。
const (
	handAmp      = 4.6  // 中线横向摆动上限
	handTilt     = 2.6  // 起收笔反向错位（看起来像笔没端平）
	handWidthLo  = 0.86 // 笔画相对原笔宽的比例
	handWidthMod = 0.16 // 沿笔画方向的笔宽起伏（±16%，像落笔压力变化）
	handEnds     = 2.0  // 起收笔的径向出头上限
)

// handRoom 返回一个点还能横向偏移多少（像素），两个约束取小：
//   - 不能偏出自己的格子：偏移 < 笔宽一半，格心才在墨迹里；
//   - 不能偏到邻行的格心上：偏移 + 笔宽一半 < 行距，邻行才留白。
//
// 极坐标里行距 = r·Δθ，内圈只有 10px、外圈 24px，所以同一枚码里
// 外圈笔画能甩得很开、内圈只能老实待着 —— 这正好也是手绘的观感。
func handRoom(pw, pitch float64) float64 {
	// 留 1.4px 的余量：格心周围一圈都要有墨，不能只蹭到一点点边
	room := pw/2 - 1.4
	if v := pitch - 1.3 - pw/2; v < room {
		room = v
	}
	if room < 0.15 {
		room = 0.15
	}
	return room
}

// handStrokes 生成整枚码的手绘笔画集合。
func handStrokes(g [72][16]byte) []inkPass {
	out := make([]inkPass, 0, 1024)
	for _, run := range gridRuns(g) {
		out = append(out, handRunStrokes(run.Row, run.Col1, run.Col2)...)
	}
	for _, c := range finderCenters {
		out = append(out, handFinderStrokes(c[0], c[1])...)
	}
	return out
}

// handRunStrokes 把一个游程（同一行连续为 1 的几个格子）画成手绘笔画。
//
// 游程在同一行上，行定角度、列定半径，所以它是一条从圆心射出的径向线段：
// 横向（切向）抖动就是"笔没画直"，径向出头就是"起收笔"。
//
// 笔画是「一笔画的」：笔宽沿长度起伏（落笔重、提笔轻），中线跟着摆，
// 两端各带一点出头；每个点的摆幅都按 handRoom 现算，所以既夸张又不会写坏。
func handRunStrokes(row, col1, col2 int) []inkPass {
	ang := float64(wxAngleStep) * float64(row)
	ux, uy := -math.Cos(ang), -math.Sin(ang) // 圆心 → 该行
	px, py := -uy, ux                        // 该行的切向

	w := wxStroke
	r1 := (float64(col1) + 10.5) * float64(wxSize)
	r2 := (float64(col2) + 10.5) * float64(wxSize)
	cells := float64(col2 - col1 + 1)
	// 行距：极坐标里相邻两行的间距随半径线性增长
	pitch := 0.5 * (r1 + r2) * float64(wxAngleStep)

	rn := newInkRand(row, col1, col2, 11)
	ph := rn.next() * 2 * math.Pi
	ph2 := rn.next() * 2 * math.Pi

	base := w * handWidthLo
	// 起笔 / 收笔的出头量：一出一进，像手落笔时的惯性
	e0 := rn.sym(handEnds)
	e1 := rn.sym(handEnds)
	single := cells < 2
	if single {
		// 单格的游程就是一笔点墨：短、略歪、边缘不圆（笔尖落纸本来就不是正圆）
		base *= 1.04
		e0 = rn.sym(0.6)
		e1 = rn.sym(0.6)
	}
	// 频率随长度增长：长笔画是几次轻微起伏，而不是一整条弓形
	freq := 1.1 + cells*0.5 + rn.sym(0.3)
	// 收笔有时提笔变细、有时顿笔变粗，两种都要有才不机械
	endK := 0.62
	if rn.next() < 0.4 {
		endK = 1.3
	}
	if single {
		endK = 1 // 点墨没有"提笔"，两端不收
	}
	// 采样点给密一点：笔宽的起伏和摆动才能连成连续的边缘，
	// 而不是一格一个"珠子"（点太疏会看着像串珠，不像笔迹）。
	segs := int(math.Ceil(cells * 4))
	if segs < 4 {
		segs = 4
	}

	pts := make([]inkPt, 0, segs+1)
	for i := 0; i <= segs; i++ {
		s := float64(i) / float64(segs)
		// 笔宽：低频起伏（落笔/提笔），两端再按 endK 收一下
		pw := base * (1 - handWidthMod + 2*handWidthMod*halfSin(ph2+s*2.4))
		if i == 0 || i == segs {
			pw *= endK
		}
		if pw > w*1.22 {
			pw = w * 1.22
		}
		room := handRoom(pw, pitch)
		if single && room > 1.3 {
			// 单个点只允许歪一点点：整颗点飘出格子会很扎眼
			room = 1.3
		}
		// 摆动（低频）+ 倾斜（两端反向）：两者合起来不超过 room
		amp, tilt := 0.72*room, 0.28*room
		if amp > handAmp {
			amp = handAmp
		}
		if tilt > handTilt {
			tilt = handTilt
		}
		swing := amp*math.Sin(ph+s*freq*math.Pi) + tilt*(2*s-1)
		rr := (r1 - e0) + (r2-r1+e0+e1)*s + 0.35*math.Sin(ph2*1.3+s*6.1)
		pts = append(pts, inkPt{
			X: wxCenter + ux*rr + px*swing,
			Y: wxCenter + uy*rr + py*swing,
			W: pw,
		})
	}
	return []inkPass{{Pts: pts}}
}

// handFinderStrokes 画一组定位图案：外环一圈墨 + 圆心一记点墨。
//
// 外环的外扩方向刻意压住（只会比经典略大一点点），避免手绘抖动把环
// 和旁边的码点粘成一个连通域 —— 解码端是靠"环"的连通域找定位图案的。
func handFinderStrokes(cx, cy float64) []inkPass {
	s := float64(wxSize)
	rn := newInkRand(int(math.Round(cx)), int(math.Round(cy)), 77)

	const steps = 96
	baseR := 2.1 * s
	baseW := 0.8 * s
	ph := rn.next() * 2 * math.Pi
	ph2 := rn.next() * 2 * math.Pi
	a0 := rn.next() * 2 * math.Pi
	// 多绕一点点：手画的圆从不刚好闭合
	sweep := 2 * math.Pi * (1.012 + rn.next()*0.014)
	oval := rn.sym(0.55)

	ring := make([]inkPt, 0, steps+1)
	for i := 0; i <= steps; i++ {
		t := float64(i) / steps
		a := a0 + sweep*t
		dr := 1.15*math.Sin(ph+a*2) + 0.55*math.Sin(ph2+a*5) + oval*math.Cos(a+ph)
		// 外扩只放一点点：环和旁边的码点粘成一个连通域，定位图案就找不到了
		if dr > 0.45 {
			dr = 0.45 + (dr-0.45)*0.3
		}
		r := baseR + dr
		ring = append(ring, inkPt{
			X: cx + math.Cos(a)*r,
			Y: cy + math.Sin(a)*r,
			W: baseW * (0.82 + 0.36*halfSin(ph2*1.7+a*3)),
		})
	}

	// 圆心点：一记短促的点墨（半径仍是 0.5 格），避免画成数学上完美的圆盘
	d := 0.5 * s
	wa := rn.next() * 2 * math.Pi
	dx, dy := math.Cos(wa), math.Sin(wa)
	const dabSteps = 8
	dab := make([]inkPt, 0, dabSteps+1)
	for i := 0; i <= dabSteps; i++ {
		t := float64(i) / dabSteps
		off := (t - 0.5) * d * 0.75
		pw := 2 * d * 1.01 * (1 + 0.04*math.Sin(ph+t*4))
		if i == 0 || i == dabSteps {
			pw *= 0.9
		}
		dab = append(dab, inkPt{
			X: cx + dx*off,
			Y: cy + dy*off,
			W: pw,
		})
	}
	return []inkPass{{Pts: ring}, {Pts: dab}}
}

// halfSin 把 sin 映射到 0..1。
func halfSin(x float64) float64 { return (math.Sin(x) + 1) / 2 }

// inkRand 是由整数种子派生的确定性伪随机序列（LCG）。
//
// 用局部状态而不是全局 rand：渲染是并发的，也没有必要为一次画图播种全局随机源。
type inkRand struct{ s uint32 }

func newInkRand(parts ...int) *inkRand {
	s := uint32(2166136261)
	for _, p := range parts {
		s ^= uint32(int32(p))
		s *= 16777619
	}
	if s == 0 {
		s = 0x9e3779b9
	}
	return &inkRand{s: s}
}

// next 返回 [0,1) 的伪随机数。
func (r *inkRand) next() float64 {
	r.s = r.s*1664525 + 1013904223
	return float64(r.s>>8) / float64(1<<24)
}

// sym 返回 [-a,a] 的伪随机数。
func (r *inkRand) sym(a float64) float64 { return (r.next()*2 - 1) * a }
