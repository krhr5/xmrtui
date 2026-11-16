package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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

const glyphH = 7

// bitmap lays out text as a 7-row pixel grid with 1px letter spacing.
func bitmap(s string) [][]bool {
	rows := make([][]bool, glyphH)
	first := true
	for _, r := range strings.ToUpper(s) {
		g, ok := glyphs[r]
		if !ok {
			g = glyphs['?']
		}
		for y := 0; y < glyphH; y++ {
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
func pixelWidth(s string, scale int, shadow bool) int {
	w := len(bitmap(s)[0]) * scale
	if shadow {
		w++
	}
	return w
}

// pixelText renders s with half-block characters so each pixel is roughly
// square. colorAt picks a colour per pixel row (for gradients); shadow is a
// drop-shadow colour ("" for none).
func pixelText(s string, scale int, colorAt func(y, h int) string, shadow string) string {
	bm := bitmap(s)
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
