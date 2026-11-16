package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type pixelFont struct {
	h      int
	glyphs map[rune][]string
}

// small is a 3x5 font (M is 5 wide, N is 4) used only when the window is
// too narrow for the heading or balance in their normal fonts.
var small = pixelFont{h: 5, glyphs: map[rune][]string{
	'0': {"###", "#.#", "#.#", "#.#", "###"},
	'1': {".#.", "##.", ".#.", ".#.", "###"},
	'2': {"###", "..#", "###", "#..", "###"},
	'3': {"###", "..#", ".##", "..#", "###"},
	'4': {"#.#", "#.#", "###", "..#", "..#"},
	'5': {"###", "#..", "###", "..#", "###"},
	'6': {"###", "#..", "###", "#.#", "###"},
	'7': {"###", "..#", ".#.", ".#.", ".#."},
	'8': {"###", "#.#", "###", "#.#", "###"},
	'9': {"###", "#.#", "###", "..#", "###"},
	'.': {".", ".", ".", ".", "#"},
	'X': {"#.#", "#.#", ".#.", "#.#", "#.#"},
	'M': {"#...#", "##.##", "#.#.#", "#...#", "#...#"},
	'R': {"##.", "#.#", "##.", "#.#", "#.#"},
	'O': {"###", "#.#", "#.#", "#.#", "###"},
	'N': {"#..#", "##.#", "#.##", "#..#", "#..#"},
	'E': {"###", "#..", "##.", "#..", "###"},
	'?': {"###", "..#", ".#.", "...", ".#."},
	' ': {".", ".", ".", ".", "."},
}}

// big is the 5x7 font used for the balance.
var big = pixelFont{h: 7, glyphs: glyphs}

// 5x7 pixel font. '#' = lit pixel.
var glyphs = map[rune][]string{
	'M': {"#...#", "##.##", "#.#.#", "#.#.#", "#...#", "#...#", "#...#"},
	'O': {".###.", "#...#", "#...#", "#...#", "#...#", "#...#", ".###."},
	'N': {"#...#", "##..#", "##..#", "#.#.#", "#..##", "#..##", "#...#"},
	'E': {"#####", "#....", "#....", "####.", "#....", "#....", "#####"},
	'R': {"####.", "#...#", "#...#", "####.", "#.#..", "#..#.", "#...#"},
	'X': {"#...#", "#...#", ".#.#.", "..#..", ".#.#.", "#...#", "#...#"},
	'0': {".###.", "#...#", "#..##", "#.#.#", "##..#", "#...#", ".###."},
	'1': {"..#..", ".##..", "..#..", "..#..", "..#..", "..#..", ".###."},
	'2': {".###.", "#...#", "....#", "...#.", "..#..", ".#...", "#####"},
	'3': {"####.", "....#", "....#", ".###.", "....#", "....#", "####."},
	'4': {"...#.", "..##.", ".#.#.", "#..#.", "#####", "...#.", "...#."},
	'5': {"#####", "#....", "####.", "....#", "....#", "#...#", ".###."},
	'6': {"..##.", ".#...", "#....", "####.", "#...#", "#...#", ".###."},
	'7': {"#####", "....#", "...#.", "..#..", ".#...", ".#...", ".#..."},
	'8': {".###.", "#...#", "#...#", ".###.", "#...#", "#...#", ".###."},
	'9': {".###.", "#...#", "#...#", ".####", "....#", "...#.", ".##.."},
	'.': {".", ".", ".", ".", ".", ".", "#"},
	'-': {"...", "...", "...", "###", "...", "...", "..."},
	'?': {".###.", "#...#", "....#", "...#.", "..#..", ".....", "..#.."},
	' ': {"..", "..", "..", "..", "..", "..", ".."},
}

// bitmap lays out text as a pixel grid with 1px letter spacing.
func (f pixelFont) bitmap(s string) [][]bool {
	rows := make([][]bool, f.h)
	first := true
	for _, r := range strings.ToUpper(s) {
		g, ok := f.glyphs[r]
		if !ok {
			g = f.glyphs['?']
		}
		for y := 0; y < f.h; y++ {
			if !first {
				rows[y] = append(rows[y], false)
			}
			for _, c := range g[y] {
				rows[y] = append(rows[y], c == '#')
			}
		}
		first = false
	}
	return rows
}

// pixelWidth returns the rendered terminal width of s at the given scale.
func pixelWidth(s string, scale int, shadow bool) int { return big.width(s, scale, shadow) }

func (f pixelFont) width(s string, scale int, shadow bool) int {
	w := len(f.bitmap(s)[0]) * scale
	if shadow {
		w++
	}
	return w
}

// pixelText renders s with half-block characters so each pixel is roughly
// square. colorAt picks a colour per pixel row (for gradients); shadow is a
// drop-shadow colour ("" for none).
func pixelText(s string, scale int, colorAt func(y, h int) string, shadow string) string {
	return big.render(s, scale, colorAt, shadow)
}

func (f pixelFont) render(s string, scale int, colorAt func(y, h int) string, shadow string) string {
	glyphH := f.h
	bm := f.bitmap(s)
	bw, bh := len(bm[0])*scale, glyphH*scale
	w, h := bw, bh
	if shadow != "" {
		w, h = w+1, h+1
	}
	if h%2 == 1 {
		h++
	}
	px := make([][]string, h)
	for y := range px {
		px[y] = make([]string, w)
	}
	plot := func(ox, oy int, col func(y int) string) {
		for y := 0; y < glyphH; y++ {
			for x, on := range bm[y] {
				if !on {
					continue
				}
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						py := y*scale + dy + oy
						px[py][x*scale+dx+ox] = col(py - oy)
					}
				}
			}
		}
	}
	if shadow != "" {
		plot(1, 1, func(int) string { return shadow })
	}
	plot(0, 0, func(y int) string { return colorAt(y, bh) })
	return renderHalfBlocks(px)
}

var halfStyles = map[[2]string]lipgloss.Style{}

func renderHalfBlocks(px [][]string) string {
	var b strings.Builder
	for y := 0; y+1 < len(px); y += 2 {
		if y > 0 {
			b.WriteByte('\n')
		}
		for x := range px[y] {
			top, bot := px[y][x], px[y+1][x]
			switch {
			case top == "" && bot == "":
				b.WriteByte(' ')
			case top == bot:
				b.WriteString(cellStyle(top, "").Render("█"))
			case top == "":
				b.WriteString(cellStyle(bot, "").Render("▄"))
			case bot == "":
				b.WriteString(cellStyle(top, "").Render("▀"))
			default:
				b.WriteString(cellStyle(top, bot).Render("▀"))
			}
		}
	}
	return b.String()
}

func cellStyle(fg, bg string) lipgloss.Style {
	k := [2]string{fg, bg}
	if s, ok := halfStyles[k]; ok {
		return s
	}
	s := lipgloss.NewStyle().Foreground(lipgloss.Color(fg))
	if bg != "" {
		s = s.Background(lipgloss.Color(bg))
	}
	halfStyles[k] = s
	return s
}

// gradient returns a colorAt func blending from a to b (top to bottom).
func gradient(a, b string) func(y, h int) string {
	var ar, ag, ab, br, bg, bb int
	fmt.Sscanf(a, "#%02x%02x%02x", &ar, &ag, &ab)
	fmt.Sscanf(b, "#%02x%02x%02x", &br, &bg, &bb)
	return func(y, h int) string {
		if h <= 1 {
			return a
		}
		t := float64(y) / float64(h-1)
		mix := func(p, q int) int { return p + int(float64(q-p)*t+0.5) }
		return fmt.Sprintf("#%02x%02x%02x", mix(ar, br), mix(ag, bg), mix(ab, bb))
	}
}

// moneroSymbol draws the Monero symbol (orange disc, white M stripe, grey
// base) at size x size pixels, using the geometry of the official 256px SVG
// and 4x4 supersampling per pixel.
func moneroSymbol(size int) string {
	const white, orange, grey = "#FFFFFF", "#FF6600", "#4C4C4C"
	classify := func(x, y float64) string {
		if math.Hypot(x-128, y-128) > 128 {
			return ""
		}
		d := math.Abs(x - 128)
		orangeEdge := 168.43
		if d < 83.2 {
			orangeEdge = 60.73 + (83.2 - d)
		}
		greyEdge := 194.5
		if d < 55.44 {
			greyEdge = 126.75 + (55.44 - d)
		}
		switch {
		case y < orangeEdge:
			return orange
		case y > greyEdge:
			return grey
		}
		return white
	}
	h := size + size%2
	px := make([][]string, h)
	const ss = 4
	for py := range px {
		px[py] = make([]string, size)
		for pxx := 0; pxx < size; pxx++ {
			votes := map[string]int{}
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(pxx) + (float64(sx)+0.5)/ss) * 256 / float64(size)
					y := (float64(py) + (float64(sy)+0.5)/ss) * 256 / float64(size)
					votes[classify(x, y)]++
				}
			}
			best, n := "", 0
			for c, v := range votes {
				if v > n || (v == n && c > best) {
					best, n = c, v
				}
			}
			px[py][pxx] = best
		}
	}
	return renderHalfBlocks(px)
}

// moneroWordmark is "MONERO" in the FIGlet font ANSI Shadow.
var moneroWordmark = []string{
	"███╗   ███╗ ██████╗ ███╗   ██╗███████╗██████╗  ██████╗",
	"████╗ ████║██╔═══██╗████╗  ██║██╔════╝██╔══██╗██╔═══██╗",
	"██╔████╔██║██║   ██║██╔██╗ ██║█████╗  ██████╔╝██║   ██║",
	"██║╚██╔╝██║██║   ██║██║╚██╗██║██╔══╝  ██╔══██╗██║   ██║",
	"██║ ╚═╝ ██║╚██████╔╝██║ ╚████║███████╗██║  ██║╚██████╔╝",
	"╚═╝     ╚═╝ ╚═════╝ ╚═╝  ╚═══╝╚══════╝╚═╝  ╚═╝ ╚═════╝",
}

// shadowArt paints ANSI-Shadow-style art: solid blocks get colorAt(line,
// total) and the box-drawing shadow strokes get the shadow colour.
func shadowArt(art []string, colorAt func(y, h int) string, shadow string) string {
	sh := lipgloss.NewStyle().Foreground(lipgloss.Color(shadow))
	out := make([]string, len(art))
	for i, l := range art {
		fg := lipgloss.NewStyle().Foreground(lipgloss.Color(colorAt(i, len(art))))
		var b strings.Builder
		var run []rune
		runBlock := false
		flush := func() {
			if len(run) == 0 {
				return
			}
			if runBlock {
				b.WriteString(fg.Render(string(run)))
			} else {
				b.WriteString(sh.Render(string(run)))
			}
			run = run[:0]
		}
		for _, r := range l {
			isBlock := r == '█' || r == ' '
			if r != ' ' && isBlock != runBlock {
				flush()
				runBlock = isBlock
			}
			run = append(run, r)
		}
		flush()
		out[i] = b.String()
	}
	return strings.Join(out, "\n")
}
