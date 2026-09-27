package artifacts

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// disc draws a filled circle with a white ring.
func disc(img *image.RGBA, cx, cy, r int, fill color.RGBA) {
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
}

// digits is a 3×5 bitmap font; each row is three bits, most significant left.
var digits = [10][5]uint8{
	{7, 5, 5, 5, 7}, {2, 6, 2, 2, 7}, {7, 1, 7, 4, 7}, {7, 1, 7, 1, 7}, {5, 5, 7, 1, 1},
	{7, 4, 7, 1, 7}, {7, 4, 7, 5, 7}, {7, 1, 1, 1, 1}, {7, 5, 7, 5, 7}, {7, 5, 7, 1, 7},
}

// maxAnnotatePixels bounds the memory for one image: decoding plus the canvas
// take 0.6 to 1 GB at the limit. A full-page screenshot at 2880 × 16384 fits.
const maxAnnotatePixels = 50_000_000

var (
	pinFill   = color.RGBA{0xc6, 0x2f, 0x35, 0xff}
	pinDone   = color.RGBA{0x6b, 0x72, 0x80, 0xff}
	pinBorder = color.RGBA{0xff, 0xff, 0xff, 0xff}
)

// Annotate writes a copy of every image with pinned comments into dir, as
// vVERSION/PATH (plus .png for other formats), with each comment's number
// drawn in a circle where it was placed. It returns the written files, keyed
// "vVERSION/PATH".
func (s Store) Annotate(p, n string, comments []Comment, dir string) (map[string]string, error) {
	groups := map[string][]Comment{}
	for _, c := range comments {
		if c.X != nil && isImage(c.Path) {
			key := fmt.Sprintf("v%d/%s", c.Version, c.Path)
			groups[key] = append(groups[key], c)
		}
	}
	written, taken := map[string]string{}, map[string]bool{}
	for key, cs := range groups {
		src, err := os.Open(filepath.Join(s.versionDir(p, n, cs[0].Version), filepath.FromSlash(cs[0].Path)))
		if err != nil {
			return written, err
		}
		// Formats without a Go decoder (webp, svg, avif) and huge images keep
		// their plain list entry.
		size, _, err := image.DecodeConfig(src)
		if err != nil || size.Width*size.Height > maxAnnotatePixels {
			src.Close()
			continue
		}
		if _, err = src.Seek(0, io.SeekStart); err != nil {
			src.Close()
			return written, err
		}
		img, _, err := image.Decode(src)
		src.Close()
		if err != nil {
			continue
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
			drawMarker(canvas, cx, cy, radius, c.Number, fill)
		}
		out := filepath.Join(dir, filepath.FromSlash(key))
		if !strings.EqualFold(filepath.Ext(out), ".png") {
			out += ".png"
		}
		if taken[out] { // x.jpg and x.jpg.png
			return written, fmt.Errorf("two images would be written to %s", out)
		}
		taken[out] = true
		if err = os.MkdirAll(filepath.Dir(out), 0700); err != nil {
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

// drawMarker draws a dot on the commented spot and the numbered circle next to
// it, up and to the right unless that leaves the image, so the spot itself
// stays visible.
func drawMarker(img *image.RGBA, cx, cy, r, number int, fill color.RGBA) {
	b := img.Bounds()
	dx, dy := r, -r
	if cx+2*r+2 > b.Max.X {
		dx = -r
	}
	if cy-2*r-2 < b.Min.Y {
		dy = r
	}
	disc(img, cx, cy, max(3, r/4), fill)
	cx, cy = cx+dx, cy+dy
	disc(img, cx, cy, r, fill)
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
