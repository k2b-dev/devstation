package artifacts

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// digits is a 3×5 bitmap font; each row is three bits, most significant left.
var digits = [10][5]uint8{
	{7, 5, 5, 5, 7}, {2, 6, 2, 2, 7}, {7, 1, 7, 4, 7}, {7, 1, 7, 1, 7}, {5, 5, 7, 1, 1},
	{7, 4, 7, 1, 7}, {7, 4, 7, 5, 7}, {7, 1, 1, 1, 1}, {7, 5, 7, 5, 7}, {7, 5, 7, 1, 7},
}

var (
	pinFill   = color.RGBA{0xe5, 0x48, 0x4d, 0xff}
	pinDone   = color.RGBA{0x88, 0x8f, 0x9a, 0xff}
	pinBorder = color.RGBA{0xff, 0xff, 0xff, 0xff}
)

// Annotate writes a copy of every image with pinned comments into dir, with
// each comment's number drawn in a circle where it was placed. It returns
// the written files, keyed "vVERSION/PATH".
func (s Store) Annotate(p, n string, comments []Comment, dir string) (map[string]string, error) {
	groups := map[string][]Comment{}
	for _, c := range comments {
		if c.X != nil && isImage(c.Path) {
			key := fmt.Sprintf("v%d/%s", c.Version, c.Path)
			groups[key] = append(groups[key], c)
		}
	}
	written := map[string]string{}
	for key, cs := range groups {
		src, err := os.Open(filepath.Join(s.versionDir(p, n, cs[0].Version), filepath.FromSlash(cs[0].Path)))
		if err != nil {
			return written, err
		}
		img, _, err := image.Decode(src)
		src.Close()
		if err != nil {
			continue // formats without a Go decoder (webp, svg, avif) keep their plain list entry
		}
		canvas := image.NewRGBA(img.Bounds())
		draw.Draw(canvas, canvas.Bounds(), img, img.Bounds().Min, draw.Src)
		b := canvas.Bounds()
		radius := max(14, min(b.Dx(), b.Dy())/30)
		for _, c := range cs {
			fill := pinFill
			if c.Resolved {
				fill = pinDone
			}
			cx := b.Min.X + int(*c.X*float64(b.Dx()))
			cy := b.Min.Y + int(*c.Y*float64(b.Dy()))
			drawPin(canvas, cx, cy, radius, c.Number, fill)
		}
		name := strings.ReplaceAll(key, "/", "_")
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".png"
		out := filepath.Join(dir, name)
		if err = os.MkdirAll(dir, 0700); err != nil {
			return written, err
		}
		f, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return written, err
		}
		err = png.Encode(f, canvas)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return written, err
		}
		written[key] = out
	}
	return written, nil
}

func drawPin(img *image.RGBA, cx, cy, r, number int, fill color.RGBA) {
	for y := -r - 2; y <= r+2; y++ {
		for x := -r - 2; x <= r+2; x++ {
			d := x*x + y*y
			switch {
			case d <= r*r:
				img.Set(cx+x, cy+y, fill)
			case d <= (r+2)*(r+2):
				img.Set(cx+x, cy+y, pinBorder)
			}
		}
	}
	text := strconv.Itoa(number)
	scale := max(1, r*9/10/5) // digit height about 90% of the radius
	width := len(text)*4*scale - scale
	x0, y0 := cx-width/2, cy-5*scale/2
	for i, ch := range text {
		glyph := digits[ch-'0']
		for row := 0; row < 5; row++ {
			for col := 0; col < 3; col++ {
				if glyph[row]&(4>>col) == 0 {
					continue
				}
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						img.Set(x0+(i*4+col)*scale+dx, y0+row*scale+dy, pinBorder)
					}
				}
			}
		}
	}
}
