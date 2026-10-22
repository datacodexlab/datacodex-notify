// SPDX-License-Identifier: GPL-3.0-only

// Command genicons draws the tray icons (brand glyph plus a colored status dot)
// and writes them as PNG-compressed .ico files.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

var iconSizes = []int{16, 24, 32, 48}

// webSizes are the PNG sizes of the favicon and the web app manifest.
var webSizes = []int{16, 32, 48, 256}

type iconSpec struct {
	name string
	dot  color.RGBA
}

var specs = []iconSpec{
	{"green", color.RGBA{0x1F, 0x9D, 0x55, 0xFF}},
	{"yellow", color.RGBA{0xE3, 0xA8, 0x2B, 0xFF}},
	{"red", color.RGBA{0xD9, 0x38, 0x38, 0xFF}},
}

var (
	brand = color.RGBA{0x2F, 0x6B, 0xFF, 0xFF}
	white = color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
)

func main() {
	outDir := flag.String("out", ".", "directory for the generated .ico files")
	webDir := flag.String("web", "", "directory for the PNG icons used by the admin web UI (skipped when empty)")
	flag.Parse()

	if *webDir != "" {
		if err := writeWebIcons(*webDir); err != nil {
			fmt.Fprintf(os.Stderr, "genicons: %v\n", err)
			os.Exit(1)
		}
	}

	for _, spec := range specs {
		data, err := buildICO(spec.dot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "genicons: %v\n", err)
			os.Exit(1)
		}
		path := filepath.Join(*outDir, "icon-"+spec.name+".ico")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "genicons: %v\n", err)
			os.Exit(1)
		}
	}
}

func buildICO(dot color.RGBA) ([]byte, error) {
	images := make([][]byte, len(iconSizes))
	for i, size := range iconSizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, render(size, dot)); err != nil {
			return nil, fmt.Errorf("encode %dpx icon: %w", size, err)
		}
		images[i] = buf.Bytes()
	}

	var out bytes.Buffer
	header := []uint16{0, 1, uint16(len(images))}
	if err := binary.Write(&out, binary.LittleEndian, header); err != nil {
		return nil, err
	}
	offset := 6 + 16*len(images)
	for i, size := range iconSizes {
		entry := struct {
			Width, Height, Colors, Reserved uint8
			Planes, BitCount                uint16
			Size, Offset                    uint32
		}{uint8(size), uint8(size), 0, 0, 1, 32, uint32(len(images[i])), uint32(offset)}
		if err := binary.Write(&out, binary.LittleEndian, entry); err != nil {
			return nil, err
		}
		offset += len(images[i])
	}
	for _, img := range images {
		out.Write(img)
	}
	return out.Bytes(), nil
}

const supersample = 4

func render(size int, dot color.RGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for sy := 0; sy < supersample; sy++ {
				for sx := 0; sx < supersample; sx++ {
					u := (float64(x) + (float64(sx)+0.5)/supersample) / float64(size)
					v := (float64(y) + (float64(sy)+0.5)/supersample) / float64(size)
					c := sample(u, v, dot)
					alpha := float64(c.A) / 255
					r += float64(c.R) * alpha
					g += float64(c.G) * alpha
					b += float64(c.B) * alpha
					a += alpha
				}
			}
			if a == 0 {
				continue
			}
			n := float64(supersample * supersample)
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(math.Round(r / a)),
				G: uint8(math.Round(g / a)),
				B: uint8(math.Round(b / a)),
				A: uint8(math.Round(a / n * 255)),
			})
		}
	}
	return img
}

// sample returns the color of the glyph at unit coordinates (u, v).
func sample(u, v float64, dot color.RGBA) color.RGBA {
	if inCircle(u, v, 0.76, 0.76, 0.16) {
		return dot
	}
	if inCircle(u, v, 0.76, 0.76, 0.22) {
		return white
	}
	if inBell(u, v) {
		return white
	}
	if inRoundedRect(u, v, 0.04, 0.04, 0.96, 0.96, 0.22) {
		return brand
	}
	return color.RGBA{}
}

func inCircle(u, v, cx, cy, r float64) bool {
	return (u-cx)*(u-cx)+(v-cy)*(v-cy) <= r*r
}

func inRoundedRect(u, v, x0, y0, x1, y1, r float64) bool {
	if u < x0 || u > x1 || v < y0 || v > y1 {
		return false
	}
	cx := math.Min(math.Max(u, x0+r), x1-r)
	cy := math.Min(math.Max(v, y0+r), y1-r)
	return inCircle(u, v, cx, cy, r)
}

func inBell(u, v float64) bool {
	const cx = 0.44
	switch {
	case inCircle(u, v, cx, 0.17, 0.045): // top knob
		return true
	case inCircle(u, v, cx, 0.40, 0.17): // dome
		return true
	case v >= 0.40 && v <= 0.60: // flared body
		half := 0.17 + (v-0.40)/0.20*0.08
		return math.Abs(u-cx) <= half
	case v > 0.60 && v <= 0.66: // rim
		return math.Abs(u-cx) <= 0.27
	case inCircle(u, v, cx, 0.70, 0.055): // clapper
		return true
	}
	return false
}

// writeWebIcons writes icon-<state>-<size>.png for every state and web size.
func writeWebIcons(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, spec := range specs {
		for _, size := range webSizes {
			var buf bytes.Buffer
			if err := png.Encode(&buf, render(size, spec.dot)); err != nil {
				return fmt.Errorf("encode %s %dpx: %w", spec.name, size, err)
			}
			name := filepath.Join(dir, fmt.Sprintf("icon-%s-%d.png", spec.name, size))
			if err := os.WriteFile(name, buf.Bytes(), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
