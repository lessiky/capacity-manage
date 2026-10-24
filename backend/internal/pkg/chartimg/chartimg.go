package chartimg

// chartimg 纯 Go 渲染简单 PNG 图表（水位条形图 / 状态分布饼图），
// 供日报 Excel/PDF 导出的"图表快照"使用（FR-RPT-12）。字体内置 SimHei（TTF）。

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed font/SimHei.ttf
var fontBytes []byte

var _ = embed.FS{}

// FontBytes 返回内置中文字体字节（PDF 嵌入用）
func FontBytes() []byte { return fontBytes }

var (
	mu      sync.Once
	faceS   font.Face // 13pt 正文
	faceB   font.Face // 15pt 标题
	faceErr error
)

func faces() (font.Face, font.Face, error) {
	mu.Do(func() {
		f, err := opentype.Parse(fontBytes)
		if err != nil {
			faceErr = fmt.Errorf("parse CJK font: %w", err)
			return
		}
		if faceS, faceErr = opentype.NewFace(f, &opentype.FaceOptions{Size: 13, DPI: 72}); faceErr != nil {
			return
		}
		if faceB, faceErr = opentype.NewFace(f, &opentype.FaceOptions{Size: 15, DPI: 72}); faceErr != nil {
			return
		}
	})
	return faceS, faceB, faceErr
}

// drawText 在 img 的 (x, y) 基线起点处绘制文本（y 为文字顶部近似位置）
func drawText(img *image.RGBA, x, y int, s string, face font.Face, c color.Color) {
	origin := fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)}
	advance := fixed.I(0)
	for _, r := range s {
		dr, mask, maskp, adv, ok := face.Glyph(fixed.Point26_6{X: origin.X + advance, Y: origin.Y}, r)
		if !ok {
			advance += adv
			continue
		}
		draw.DrawMask(img, dr, image.NewUniform(c), image.Point{}, mask, maskp, draw.Over)
		advance += adv
	}
}

// textWidth 文本像素宽度（近似）
func textWidth(s string, face font.Face) int {
	total := fixed.I(0)
	for _, r := range s {
		adv, _ := face.GlyphAdvance(r)
		total += adv
	}
	return int(total.Round())
}

// truncateText 按 rune 截断并加省略号
func truncateText(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n-1]) + "…"
}

// BarItem 水位条形图条目
type BarItem struct {
	Label string
	Value float64 // 水位（0~1, 可 >1）
	Color color.Color
}

var (
	ColorNormal  = color.RGBA{0x34, 0xc7, 0x59, 0xff}
	ColorWarn    = color.RGBA{0xff, 0x95, 0x00, 0xff}
	ColorDanger  = color.RGBA{0xff, 0x3b, 0x30, 0xff}
	ColorMissing = color.RGBA{0x90, 0x93, 0x99, 0xff}
	ColorGrid    = color.RGBA{0xd9, 0xd9, 0xd9, 0xff}
	ColorAxis    = color.RGBA{0x99, 0x99, 0x99, 0xff}
	ColorText    = color.RGBA{0x33, 0x33, 0x33, 0xff}
)

// WaterLevelBars 水平条形图: 指标水位 TOP N
func WaterLevelBars(items []BarItem, w, h int) (*image.RGBA, error) {
	face, faceBig, err := faces()
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	if len(items) == 0 {
		drawText(img, w/2-20, h/2-8, "暂无数据", face, ColorAxis)
		return img, nil
	}
	left := 130
	right := 64
	top := 34
	bottom := 26
	plotW := w - left - right
	rowH := (h - top - bottom) / len(items)
	if rowH < 14 {
		rowH = 14
	}
	scale := 1.0
	for _, it := range items {
		if it.Value > scale {
			scale = it.Value
		}
	}
	drawText(img, 8, 8, "水位 TOP 指标（%）", faceBig, ColorText)
	for _, g := range []float64{0.8, 0.9} {
		if g > scale {
			continue
		}
		x := left + int(float64(plotW)*g/scale)
		for y := top; y < h-bottom; y += 2 {
			img.Set(x, y, ColorGrid)
		}
		drawText(img, x-18, h-20, fmt.Sprintf("%d%%", int(g*100)), face, ColorAxis)
	}
	for i, it := range items {
		y := top + i*rowH + rowH/2
		label := truncateText(it.Label, 11)
		drawText(img, left-textWidth(label, face)-6, y-9, label, face, ColorText)
		bw := int(float64(plotW) * math.Max(0, math.Min(it.Value, scale)) / scale)
		if bw < 1 && it.Value > 0 {
			bw = 1
		}
		draw.Draw(img, image.Rect(left, y-7, left+bw, y+7), image.NewUniform(it.Color), image.Point{}, draw.Src)
		drawText(img, left+bw+6, y-9, fmt.Sprintf("%.1f%%", it.Value*100), face, ColorText)
	}
	return img, nil
}

// StatusCounts 指标状态计数
type StatusCounts struct {
	Normal  int
	Warn    int
	Danger  int
	Missing int
}

// StatusDonut 状态分布环形图 + 图例
func StatusDonut(c StatusCounts, w, h int) (*image.RGBA, error) {
	face, faceBig, err := faces()
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	drawText(img, 8, 8, "指标状态分布", faceBig, ColorText)
	total := c.Normal + c.Warn + c.Danger + c.Missing
	if total == 0 {
		drawText(img, w/2-20, h/2-8, "暂无数据", face, ColorAxis)
		return img, nil
	}
	cx, cy, radius := 130, h/2+12, 86
	type seg struct {
		n   int
		col color.Color
		lab string
	}
	segs := []seg{
		{c.Normal, ColorNormal, "正常"},
		{c.Warn, ColorWarn, "预警"},
		{c.Danger, ColorDanger, "危险"},
		{c.Missing, ColorMissing, "缺数"},
	}
	ang := -math.Pi / 2
	for _, s := range segs {
		if s.n == 0 {
			continue
		}
		sweep := 2 * math.Pi * float64(s.n) / float64(total)
		fillWedge(img, cx, cy, radius, ang, ang+sweep, s.col)
		ang += sweep
	}
	cut := int(float64(radius) * 5 / 8)
	circleFill(img, cx, cy, cut, color.White)
	tot := fmt.Sprintf("%d", total)
	drawText(img, cx-textWidth(tot, faceBig)/2, cy-18, tot, faceBig, ColorText)
	drawText(img, cx-10, cy+2, "指标", face, ColorAxis)
	lx := 250
	for i, s := range segs {
		y := h/2 - 44 + i*26
		draw.Draw(img, image.Rect(lx, y, lx+14, y+14), image.NewUniform(s.col), image.Point{}, draw.Src)
		drawText(img, lx+22, y-2, fmt.Sprintf("%s %d", s.lab, s.n), face, ColorText)
	}
	return img, nil
}
func fillWedge(img *image.RGBA, cx, cy, r int, a0, a1 float64, c color.Color) {
	steps := int(math.Max(8, (a1-a0)*60))
	prev := [2]float64{float64(cx) + float64(r)*math.Cos(a0), float64(cy) + float64(r)*math.Sin(a0)}
	for i := 1; i <= steps; i++ {
		a := a0 + (a1-a0)*float64(i)/float64(steps)
		x := float64(cx) + float64(r)*math.Cos(a)
		y := float64(cy) + float64(r)*math.Sin(a)
		fillTri(img, cx, cy, int(prev[0]), int(prev[1]), int(x), int(y), c)
		prev[0], prev[1] = x, y
	}
}
func fillTri(img *image.RGBA, x0, y0, x1, y1, x2, y2 int, c color.Color) {
	minX, maxX := min3(x0, x1, x2), max3(x0, x1, x2)
	minY, maxY := min3(y0, y1, y2), max3(y0, y1, y2)
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			if pointInTri(x, y, x0, y0, x1, y1, x2, y2) {
				img.Set(x, y, c)
			}
		}
	}
}
func pointInTri(px, py, x0, y0, x1, y1, x2, y2 int) bool {
	d1 := cross(px, py, x0, y0, x1, y1)
	d2 := cross(px, py, x1, y1, x2, y2)
	d3 := cross(px, py, x2, y2, x0, y0)
	hasNeg := d1 < 0 || d2 < 0 || d3 < 0
	hasPos := d1 > 0 || d2 > 0 || d3 > 0
	return !(hasNeg && hasPos)
}
func cross(px, py, x0, y0, x1, y1 int) int {
	return (x1-x0)*(py-y0) - (px-x0)*(y1-y0)
}
func circleFill(img *image.RGBA, cx, cy, r int, c color.Color) {
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r {
				img.Set(x, y, c)
			}
		}
	}
}
func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
func max3(a, b, c int) int {
	if a > b {
		if a > c {
			return a
		}
		return c
	}
	if b > c {
		return b
	}
	return c
}

// PNG 编码为字节
func PNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
