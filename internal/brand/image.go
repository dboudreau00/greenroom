package brand

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MaxAssetBytes caps a stored asset.
const MaxAssetBytes = 1 << 20

// ImageType identifies an asset by its content, never by what the server
// claimed: PNG, JPEG, GIF, WebP, ICO or a safe SVG. It returns the media
// type and a file extension.
func ImageType(b []byte) (string, string, error) {
	if len(b) == 0 {
		return "", "", errors.New("empty file")
	}
	if len(b) > MaxAssetBytes {
		return "", "", fmt.Errorf("image is %d bytes, the limit is %d", len(b), MaxAssetBytes)
	}
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", ".png", nil
	case bytes.HasPrefix(b, []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg", ".jpg", nil
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return "image/gif", ".gif", nil
	case len(b) > 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp", ".webp", nil
	case bytes.HasPrefix(b, []byte{0, 0, 1, 0}):
		return "image/x-icon", ".ico", nil
	}
	if looksLikeSVG(b) {
		if err := CheckSVG(b); err != nil {
			return "", "", err
		}
		return "image/svg+xml", ".svg", nil
	}
	return "", "", fmt.Errorf("not a supported image (%s)", http.DetectContentType(b))
}

func looksLikeSVG(b []byte) bool {
	head := strings.ToLower(string(b[:min(len(b), 1024)]))
	return strings.Contains(head, "<svg")
}

var (
	svgEventRe = regexp.MustCompile(`(?i)[\s"'/]on[a-z]+\s*=`)
	svgHrefRe  = regexp.MustCompile(`(?i)(?:xlink:)?href\s*=\s*["']?\s*([^"'\s>]*)`)
	svgURLRe   = regexp.MustCompile(`(?i)url\(\s*["']?\s*([^"')\s]*)`)
)

// CheckSVG refuses SVG documents that could run script or pull in anything
// from outside when opened: script elements, event handler attributes,
// javascript: links, foreignObject, entity declarations, and any href or
// url() that is not a fragment or an inline raster image. It errs on the
// side of refusing; the fetch then tries the next logo candidate.
func CheckSVG(b []byte) error {
	if len(b) > 256<<10 {
		return errors.New("svg larger than 256 KiB")
	}
	s := strings.ToLower(string(b))
	for _, bad := range []string{"<script", "javascript:", "vbscript:", "<foreignobject", "<!entity", "<iframe", "<embed", "<object", "@import", "<?xml-stylesheet", "<handler", "<listener", "<set ", "<animate"} {
		if strings.Contains(s, bad) {
			return fmt.Errorf("svg refused: contains %s", strings.TrimSpace(bad))
		}
	}
	if svgEventRe.MatchString(s) {
		return errors.New("svg refused: event handler attribute")
	}
	refs := append(svgHrefRe.FindAllStringSubmatch(s, -1), svgURLRe.FindAllStringSubmatch(s, -1)...)
	for _, m := range refs {
		ref := strings.TrimSpace(m[1])
		if ref == "" || strings.HasPrefix(ref, "#") {
			continue
		}
		if strings.HasPrefix(ref, "data:image/png") || strings.HasPrefix(ref, "data:image/jpeg") || strings.HasPrefix(ref, "data:image/gif") || strings.HasPrefix(ref, "data:image/webp") {
			continue
		}
		return errors.New("svg refused: external or non-image reference")
	}
	return nil
}

// DominantColors returns up to n saturated colors that dominate a raster
// image, most common first. Transparent, near-white, near-black and grey
// pixels are ignored. SVG and formats without a stdlib decoder give none.
func DominantColors(b []byte, n int) []string {
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w == 0 || h == 0 {
		return nil
	}
	step := int(math.Max(1, math.Sqrt(float64(w*h)/40000)))
	type bucket struct {
		count   int
		r, g, b int
	}
	buckets := map[int]*bucket{}
	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			r16, g16, b16, a16 := img.At(x, y).RGBA()
			if a16 < 0x8000 {
				continue
			}
			// Un-premultiply, then go to 8 bits.
			r, g, bl := int(r16*0xffff/a16)>>8, int(g16*0xffff/a16)>>8, int(b16*0xffff/a16)>>8
			mx, mn := max(r, g, bl), min(r, g, bl)
			if mx < 40 || mn > 225 || mx-mn < 40 {
				continue
			}
			key := (r>>4)<<8 | (g>>4)<<4 | bl>>4
			bk := buckets[key]
			if bk == nil {
				bk = &bucket{}
				buckets[key] = bk
			}
			bk.count++
			bk.r += r
			bk.g += g
			bk.b += bl
		}
	}
	list := make([]*bucket, 0, len(buckets))
	for _, bk := range buckets {
		list = append(list, bk)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].count > list[j].count })
	var out []string
	var picked [][3]int
	for _, bk := range list {
		c := [3]int{bk.r / bk.count, bk.g / bk.count, bk.b / bk.count}
		distinct := true
		for _, p := range picked {
			if abs(p[0]-c[0])+abs(p[1]-c[1])+abs(p[2]-c[2]) < 90 {
				distinct = false
			}
		}
		if !distinct {
			continue
		}
		picked = append(picked, c)
		out = append(out, hexColor(c[0], c[1], c[2]))
		if len(out) == n {
			break
		}
	}
	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Shade scales a #rrggbb color towards black by factor (0..1).
func Shade(hex string, factor float64) string {
	if len(hex) != 7 || hex[0] != '#' {
		return hex
	}
	var c [3]int
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseUint(hex[1+i*2:3+i*2], 16, 8)
		if err != nil {
			return hex
		}
		c[i] = int(math.Round(float64(v) * factor))
	}
	return hexColor(c[0], c[1], c[2])
}
