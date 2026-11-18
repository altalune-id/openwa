package web

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
)

func TestFavicons_UseTheOneBrandGold(t *testing.T) {
	svg, err := staticFS.ReadFile("static/openwa-mark-16.svg")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(svg), `stroke="#ffbb00"`) {
		t.Error("openwa-mark-16.svg must stroke #ffbb00")
	}
	for _, name := range []string{"static/favicon-16x16.png", "static/favicon-32x32.png"} {
		b, err := staticFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		counts := map[[3]uint32]int{}
		for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
			for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
				r, g, b, a := img.At(x, y).RGBA()
				if a == 0xffff {
					counts[[3]uint32{r >> 8, g >> 8, b >> 8}]++
				}
			}
		}
		var top [3]uint32
		for c, n := range counts {
			if n > counts[top] {
				top = c
			}
		}
		if top != [3]uint32{0xff, 0xbb, 0x00} {
			t.Errorf("%s: main opaque color is #%02x%02x%02x, want #ffbb00", name, top[0], top[1], top[2])
		}
	}
}
