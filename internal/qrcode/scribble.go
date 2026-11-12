package qrcode

import "math"

// 笔画风格的墨线几何（600×600 坐标系，PNG 与 SVG 共用）。
//
// 手绘是「一笔带过」：一条顺滑的墨线，笔宽低频起伏。笔画风反过来 ——
// 一段游程是一小片被反复涂过的墨：
//
//  1. 先打一道细的实心底，负责格心落墨、保证码扫得出来；
//  2. 再在上面叠 3~5 道随机短划：每道自己定长度、自己偏一点、自己收笔，
//     划不到头和划过了头的末端就成了边上的毛刺和飞白；
//  3. 末了挑一两根细毛须，像干笔拖过纸面留下的纤维。
//
// 为什么不用「一条笔画 + 逐点随机笔宽」：胶囊两端永远是圆的，
// 粗细一变就被圆角抹平，看着像一坨光滑的面包。改成叠短划之后，
// 粗糙度来自**划线的末端**，那是尖的，才像马克笔/蜡笔在纸上蹭。
//
// 底线和手绘一致：每个点能偏多少由 handRoom 现算（既要盖住自己的格心，
// 又不能蹭到邻行的格心），噪声由 (行,列) 散列出的确定性 LCG 驱动 ——
// 同一个码永远画出同一张图。

// 散列盐：让笔画风与手绘的噪声错开，否则同一枚码在两种风格下的
// 抖动位置完全一样，一眼就能看出是同一套底噪。
const (
	scrRunSalt    = 43
	scrFinderSalt = 71
)

const (
	scrCoreW    = 0.92 // 打心底宽（相对笔宽）：只负责格心落墨
	scrCoreJit  = 0.12 // 打底笔宽的逐点抖动（轻，太抖会把底抖断）
	scrDashMin  = 3    // 每段游程至少叠几道短划
	scrDashMax  = 2    // 再随机多叠 0~2 道
	scrDashW    = 0.86 // 短划笔宽上限（相对笔宽）
	scrDashOff  = 2.4  // 短划沿切向的偏移上限（像素）
	scrDashOver = 3.0  // 短划起收笔的出头/缩进上限（像素）
	scrBristles = 2    // 每段游程最多挑几根毛须
	scrBristW   = 0.16 // 毛须根部笔宽（占笔宽比例）
	scrSegsPer  = 4.0  // 每格采样点数
	scrMinSegs  = 6
	// 飞白：涂过的地方仍留几道白缝。安全边界——白缝一律压在墨带外侧、
	// 离中线 ≥3.2px，而解码是按格心 1.2px 半径采样，所以擦不到采样点。
	scrGapOff = 3.2 // 白缝离笔画中线的切向距离（像素）
	scrGapW   = 2.2 // 白缝最宽（像素）：再宽就把笔画切成两条细线
)

// scrStroke 是「涂一下」的参数：一段沿径向走的笔画。
type scrStroke struct {
	w    float64 // 基准笔宽
	r0   float64 // 起笔半径
	r1   float64 // 收笔半径
	off  float64 // 整笔沿切向的偏移
	sway float64 // 中段摆动幅度
	tilt float64 // 起收笔反向错位（看起来像笔没端平）
	ph   float64 // 摆动相位
	fq   float64 // 摆动频率（沿笔画的周期数）
	lift float64 // 低频粗细起伏（落笔重、提笔轻）
	jit  float64 // 笔宽逐点抖动幅度
	tap  float64 // 两端收细比例（0 = 平头）
}

// pts 把参数铺成一条可光栅化的墨线（dir 是径向单位向量，tan 是切向）。
//
// 摆动用 tanh 压进 room：越界时平滑地贴住上限，而不是硬截断 ——
// 硬截断会在笔画边上留出一道笔直的「台阶」。
func (p scrStroke) pts(segs int, pitch float64, dir, tan [2]float64, rn *inkRand) []inkPt {
	out := make([]inkPt, 0, segs+1)
	for i := 0; i <= segs; i++ {
		s := float64(i) / float64(segs)
		// 低频：落笔重、提笔轻
		w := p.w * (1 + p.lift*math.Sin(p.ph*1.7+s*2.2))
		if p.jit > 0 {
			w *= 1 - p.jit/2 + p.jit*rn.next()
		}
		if p.tap > 0 {
			e := 2*s - 1
			w *= 1 - p.tap*e*e
		}
		room := handRoom(w, pitch)
		raw := p.off + p.sway*math.Sin(p.ph+s*p.fq*2*math.Pi) + p.tilt*(2*s-1)
		swing := room * math.Tanh(raw/room)
		r := p.r0 + (p.r1-p.r0)*s
		out = append(out, inkPt{
			X: wxCenter + dir[0]*r + tan[0]*swing,
			Y: wxCenter + dir[1]*r + tan[1]*swing,
			W: w,
		})
	}
	return out
}

// scribbleStrokes 生成整枚码的笔画（含定位图案）。
func scribbleStrokes(g [72][16]byte) []inkPass {
	out := make([]inkPass, 0, 2048)
	for _, run := range gridRuns(g) {
		out = append(out, scribbleRunStrokes(run.Row, run.Col1, run.Col2)...)
	}
	for _, c := range finderCenters {
		out = append(out, scribbleFinderStrokes(c[0], c[1])...)
	}
	return out
}

// scrFrame 是某一行（一个游程）的局部坐标系：径向方向、切向方向与行距。
type scrFrame struct {
	dir, tan [2]float64
	pitch    float64
	w        float64
}

// frameOf 算出某一行、某段列区间的局部坐标系。
func frameOf(row, col1, col2 int) scrFrame {
	ang := float64(wxAngleStep) * float64(row)
	dir := [2]float64{-math.Cos(ang), -math.Sin(ang)}
	tan := [2]float64{-dir[1], dir[0]}
	r1 := (float64(col1) + 10.5) * float64(wxSize)
	r2 := (float64(col2) + 10.5) * float64(wxSize)
	return scrFrame{
		dir: dir,
		tan: tan,
		// 行距：极坐标里相邻两行的间距随半径线性增长（内圈 10px、外圈 24px）
		pitch: 0.5 * (r1 + r2) * float64(wxAngleStep),
		w:     wxStroke,
	}
}

// scribbleRunStrokes 把一个游程涂成一小片墨。
//
// 游程在同一行上，行定角度、列定半径，所以它是一条从圆心射出的径向线段。
// 一道细实心底 + 几道随机短划：短划的起收笔位置各不相同，露在底外的
// 那些末端就是笔画风的毛刺，也正是「反复涂」的痕迹。
func scribbleRunStrokes(row, col1, col2 int) []inkPass {
	f := frameOf(row, col1, col2)
	r1 := (float64(col1) + 10.5) * float64(wxSize)
	r2 := (float64(col2) + 10.5) * float64(wxSize)
	cells := float64(col2 - col1 + 1)

	rn := newInkRand(row, col1, col2, scrRunSalt)
	if cells < 2 {
		return scribbleDab(rn, r1, f)
	}

	segs := int(math.Ceil(cells * scrSegsPer))
	if segs < scrMinSegs {
		segs = scrMinSegs
	}
	// 起收笔的出头量：一出一进，像手落笔时的惯性
	e0 := rn.sym(0.9)
	e1 := rn.sym(0.9)

	passes := make([]inkPass, 0, 8)

	// 打底：实心、几乎不抖 —— 每个格心都落在它里面，码才扫得出来。
	// 宽度逐段随机（0.74~1.06 笔宽）：参考图里笔画有的细、有的糊成一坨，
	// 全是同一个宽度反而像印刷体。
	passes = append(passes, inkPass{Pts: scrStroke{
		w:    f.w * (scrCoreW + rn.sym(0.16)),
		r0:   r1 - e0*0.4,
		r1:   r2 + e1*0.4,
		off:  rn.sym(0.25),
		sway: 0.25,
		tilt: 0.20,
		ph:   rn.next() * 2 * math.Pi,
		fq:   0.6 + rn.next()*0.5,
		lift: 0.04 + rn.next()*0.06,
		jit:  scrCoreJit,
	}.pts(segs, f.pitch, f.dir, f.tan, rn)})

	// 短划：每道自己定粗细、自己偏一点、自己收笔。
	// 偏移与出头都压在 2~3px 内：够毛糙，又不会蹭到邻行的格心。
	n := scrDashMin + int(rn.next()*float64(scrDashMax+1))
	for k := 0; k < n; k++ {
		bw := f.w * (0.28 + rn.next()*(scrDashW-0.28))
		side := 1.0
		if rn.next() < 0.5 {
			side = -1
		}
		passes = append(passes, inkPass{Pts: scrStroke{
			w:    bw,
			r0:   r1 + side*rn.next()*1.2 + rn.sym(scrDashOver),
			r1:   r2 + rn.sym(scrDashOver),
			off:  side * rn.next() * scrDashOff,
			sway: 0.6,
			tilt: side * rn.sym(1.2),
			ph:   rn.next() * 2 * math.Pi,
			fq:   0.8 + rn.next()*1.4,
			lift: 0.10 + rn.next()*0.14,
			jit:  0.40,
			tap:  rn.next() * 0.18,
		}.pts(segs, f.pitch, f.dir, f.tan, rn)})
	}

	// 毛须：贴着长边挑出去一两根细丝，干笔拖过纸面的那种纤维感
	for i := 0; i < scrBristles; i++ {
		if rn.next() < 0.45 {
			continue
		}
		passes = append(passes, scrBristle(rn, r1, r2, f))
	}

	// 飞白：反复涂过之后地上仍留着几道白缝 —— 干笔没吃进纸里的那几丝。
	// 白缝一律贴着笔画外侧抹（离中线 ≥3px），格心那一竖条永远留给实心的底。
	// 段越长缝越多：长笔画本来就更容易涂不匀。
	nGap := 4 + int(rn.next()*2.99) + int(cells*0.6)
	for i := 0; i < nGap; i++ {
		passes = append(passes, scrGap(rn, r1, r2, f))
	}

	// 收笔的细挑：不常出现，细得像笔尖带出来的一根毫毛
	if rn.next() < 0.45 {
		passes = append(passes, scrFlick(rn, r2+e1, 1, f))
	}
	if rn.next() < 0.35 {
		passes = append(passes, scrFlick(rn, r1-e0, -1, f))
	}
	return passes
}

// scrGap 是涂痕里的一道白色空隙（飞白）：一段没吃到墨的地方。
//
// 长短都做：多数是 3~11px 的一小截，三成会拉长到二十来像素，
// 长的那几道还带一点弧（纸纹让笔走偏），不然就成了一刀切的直线。
// 方向与笔画偏开十几到三十几度，位置压在墨带外侧（离中线 ≥3px，碰不到格心）。
func scrGap(rn *inkRand, r0, r1 float64, f scrFrame) inkPass {
	s := 0.10 + rn.next()*0.80
	r := r0 + (r1-r0)*s
	side := 1.0
	if rn.next() < 0.5 {
		side = -1
	}
	off := side * (scrGapOff + rn.next()*1.5)
	// 长度：多数中等，三成拉长
	l := 3.0 + rn.next()*rn.next()*8.0
	if rn.next() < 0.35 {
		l *= 2.0 // 纸上总有几处干得比较透
	}
	// 方向：沿着笔画，再偏开 ±0.55 rad
	ang := rn.sym(0.55)
	ca, sa := math.Cos(ang), math.Sin(ang)
	dx := f.dir[0]*ca + f.tan[0]*sa
	dy := f.dir[1]*ca + f.tan[1]*sa
	w := 0.9 + rn.next()*(scrGapW-0.9)
	// 弧：中段往侧面兜一点，两端仍落在原处
	bend := rn.sym(0.9)
	sx := wxCenter + f.dir[0]*r + f.tan[0]*off
	sy := wxCenter + f.dir[1]*r + f.tan[1]*off
	const steps = 5
	pts := make([]inkPt, 0, steps+1)
	for i := 0; i <= steps; i++ {
		t := float64(i) / steps
		// 两端收细：白缝是中间宽两头尖的一条
		wv := w * math.Pow(math.Sin(math.Pi*t), 0.7)
		if wv < 0.35 {
			wv = 0.35 // 太细的缝在 600px 会被抗锯齿抹掉，留个下限
		}
		b := bend * math.Sin(math.Pi*t)
		pts = append(pts, inkPt{
			X: sx + dx*l*(t-0.5) + f.tan[0]*b,
			Y: sy + dy*l*(t-0.5) + f.tan[1]*b,
			W: wv,
		})
	}
	return inkPass{Pts: pts, Erase: true}
}

// scrBristle 从笔画长边上挑出一根细丝：起点在边缘内侧，斜着往外走、越走越细。
func scrBristle(rn *inkRand, r0, r1 float64, f scrFrame) inkPass {
	s := 0.15 + rn.next()*0.7
	r := r0 + (r1-r0)*s
	side := 1.0
	if rn.next() < 0.5 {
		side = -1
	}
	// 起点贴着边缘（往内留一点，确保和主体连在一起）
	edge := side * (f.w*0.30 + rn.sym(0.5))
	// 方向：径向为主，再往侧面斜出去
	ax := f.dir[0] + side*rn.sym(0.9)*f.tan[0]
	ay := f.dir[1] + side*rn.sym(0.9)*f.tan[1]
	n := math.Hypot(ax, ay)
	if n == 0 {
		return inkPass{}
	}
	ax, ay = ax/n, ay/n
	l := f.w * (0.18 + rn.next()*0.26)
	sx := wxCenter + f.dir[0]*r + f.tan[0]*edge
	sy := wxCenter + f.dir[1]*r + f.tan[1]*edge
	const steps = 3
	pts := make([]inkPt, 0, steps+1)
	for i := 0; i <= steps; i++ {
		t := float64(i) / steps
		pts = append(pts, inkPt{
			X: sx + ax*l*t,
			Y: sy + ay*l*t,
			W: f.w * scrBristW * (1 - 0.75*t),
		})
	}
	return inkPass{Pts: pts}
}

// scrFlick 是一道收笔的细挑：从笔画末端往外甩一小段，笔尖渐细。
func scrFlick(rn *inkRand, r, out float64, f scrFrame) inkPass {
	// 甩的方向 = 径向（out 决定朝里/朝外）再偏一点切向
	dx := out*f.dir[0] + rn.sym(0.8)*f.tan[0]
	dy := out*f.dir[1] + rn.sym(0.8)*f.tan[1]
	n := math.Hypot(dx, dy)
	if n == 0 {
		return inkPass{}
	}
	dx, dy = dx/n, dy/n
	l := f.w * (0.12 + rn.next()*0.28)
	sx := wxCenter + f.dir[0]*r
	sy := wxCenter + f.dir[1]*r
	const steps = 3
	pts := make([]inkPt, 0, steps+1)
	for i := 0; i <= steps; i++ {
		t := float64(i) / steps
		pts = append(pts, inkPt{
			X: sx + dx*l*t,
			Y: sy + dy*l*t,
			W: f.w * (0.26 - 0.20*t),
		})
	}
	return inkPass{Pts: pts}
}

// scribbleDab 画单格游程：一记歪点。
//
// 笔尖顿下来的点不是正圆：主点稍微拉长，再在旁边补两道短划，
// 于是轮廓被啃出缺口、边上有小疙瘩，成了那种「涂了一下」的墨点。
func scribbleDab(rn *inkRand, r float64, f scrFrame) []inkPass {
	cx := wxCenter + f.dir[0]*r
	cy := wxCenter + f.dir[1]*r

	// 主点：朝向随便取（笔尖落在纸上时没有方向），略微拉长
	a := rn.next() * 2 * math.Pi
	dx, dy := math.Cos(a), math.Sin(a)
	d := f.w * 0.92
	h := d * 0.09
	main := make([]inkPt, 0, 3)
	for i := 0; i <= 2; i++ {
		t := float64(i)/2 - 0.5
		main = append(main, inkPt{
			X: cx + dx*2*h*t,
			Y: cy + dy*2*h*t,
			W: d * (0.94 + 0.10*rn.next()),
		})
	}

	out := []inkPass{{Pts: main}}
	// 两道短划从点的边缘探出去一点：轮廓就毛了
	for k := 0; k < 2; k++ {
		oa := a + rn.sym(1.6)
		off := d * (0.16 + rn.next()*0.12)
		l := d * (0.10 + rn.next()*0.16)
		ox, oy := math.Cos(oa), math.Sin(oa)
		pts := []inkPt{
			{X: cx + ox*off, Y: cy + oy*off, W: f.w * (0.30 + 0.14*rn.next())},
			{X: cx + ox*(off+l), Y: cy + oy*(off+l), W: f.w * 0.16},
		}
		out = append(out, inkPass{Pts: pts})
	}

	// 点上几乎都留一丝没吃到墨的白：离点心 3px 开外，碰不到采样点
	gaps := 1
	if rn.next() < 0.55 {
		gaps = 2
	}
	for k := 0; k < gaps; k++ {
		ga := a + rn.sym(1.4)
		gx, gy := math.Cos(ga), math.Sin(ga)
		gd := f.w * (0.34 + rn.next()*0.10)
		gl := 1.6 + rn.next()*1.8
		gw := 0.7 + rn.next()*0.5
		sx := cx + gx*gd
		sy := cy + gy*gd
		gap := make([]inkPt, 0, 4)
		for i := 0; i <= 3; i++ {
			t := float64(i) / 3
			gap = append(gap, inkPt{
				X: sx + gx*gl*(t-0.5),
				Y: sy + gy*gl*(t-0.5),
				W: gw * math.Sin(math.Pi*t),
			})
		}
		out = append(out, inkPass{Pts: gap, Erase: true})
	}
	return out
}

// scribbleFinderStrokes 画一组定位图案：一道描歪的圆环 + 两圈没对上的补笔 + 圆心一记点墨。
//
// 圆环的半径带低频走形（画不圆）+ 逐点毛刺（笔糙），外扩方向刻意压住：
// 环和旁边的码点粘成一个连通域，解码端就找不到定位图案了。
func scribbleFinderStrokes(cx, cy float64) []inkPass {
	s := float64(wxSize)
	rn := newInkRand(int(math.Round(cx)), int(math.Round(cy)), scrFinderSalt)

	baseR := 2.1 * s
	ph := rn.next() * 2 * math.Pi
	ph2 := rn.next() * 2 * math.Pi
	a0 := rn.next() * 2 * math.Pi
	// 多绕一点点：手画的圆从不刚好闭合
	sweep := 2 * math.Pi * (1.008 + rn.next()*0.014)

	ring := scrRingPts(rn, cx, cy, baseR, 0.78*s, a0, sweep, ph, ph2, 0.6)
	out := []inkPass{{Pts: ring}}

	// 补笔：半径略偏、笔宽更细，各绕大半圈，起止角各不相同 ——
	// 像描第二三遍时没和第一遍对上，环的外缘因此裂成几层。
	for k := 0; k < 2; k++ {
		sw := 2 * math.Pi * (0.55 + rn.next()*0.3)
		ra := baseR + (float64(k)*2-1)*0.12*s
		out = append(out, inkPass{Pts: scrRingPts(rn, cx, cy, ra, 0.40*s,
			a0+rn.next()*2*math.Pi, sw, ph2*1.7, ph*2.3, 0.35)})
	}

	out = append(out, inkPass{Pts: scrFinderDab(cx, cy, s, rn)})
	return out
}

// scrRingPts 沿一个「画不圆」的圆铺一圈点。
//
// 半径抖动分三层：低频走形、中频起伏、逐点毛刺；正向（外扩）严格钳在
// +0.45px 内，负向也收到 -0.9px —— 内缩只是把中间的白洞缩小一点，安全。
func scrRingPts(rn *inkRand, cx, cy, baseR, baseW, a0, sweep, ph, ph2, amp float64) []inkPt {
	const steps = 140
	pts := make([]inkPt, 0, steps+1)
	for i := 0; i <= steps; i++ {
		t := float64(i) / steps
		a := a0 + sweep*t
		dr := amp * (1.05*math.Sin(ph+a*2) + 0.7*math.Sin(ph2+a*7))
		// 逐点毛刺：让环的外缘也带锯齿
		if rn.next() < 0.18 {
			dr += 0.55 * rn.next()
		}
		if dr > 0.45 {
			dr = 0.45 + (dr-0.45)*0.3
		} else if dr < -0.9 {
			dr = -0.9
		}
		r := baseR + dr
		w := baseW * (0.84 + 0.32*rn.next())
		pts = append(pts, inkPt{
			X: cx + math.Cos(a)*r,
			Y: cy + math.Sin(a)*r,
			W: w,
		})
	}
	return pts
}

// scrFinderDab 是定位图案的圆心：一记短促的点墨，不用数学上完美的圆盘。
func scrFinderDab(cx, cy, s float64, rn *inkRand) []inkPt {
	d := 0.5 * s
	wa := rn.next() * 2 * math.Pi
	dx, dy := math.Cos(wa), math.Sin(wa)
	const steps = 6
	pts := make([]inkPt, 0, steps+1)
	for i := 0; i <= steps; i++ {
		t := float64(i)/steps - 0.5
		wt := 2 * d * 0.98 * (0.92 + 0.16*rn.next())
		pts = append(pts, inkPt{
			X: cx + dx*d*0.16*t,
			Y: cy + dy*d*0.16*t,
			W: wt,
		})
	}
	return pts
}
