package env

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strings"
)

var glyphs = map[rune]string{
	'0': "01110100011001110101110011000101110", '1': "00100011000010000100001000010001110", '2': "01110100010000100010001000100011111", '3': "11110000010000101110000010000111110", '4': "00010001100101010010111110001000010", '5': "11111100001000011110000010000111110", '6': "01110100001000011110100011000101110", '7': "11111000010001000100010000100001000", '8': "01110100011000101110100011000101110", '9': "01110100011000101111000010000101110",
	'A': "01110100011000111111100011000110001", 'B': "11110100011000111110100011000111110", 'C': "01111100001000010000100001000001111", 'D': "11110100011000110001100011000111110", 'E': "11111100001000011110100001000011111", 'F': "11111100001000011110100001000010000", 'G': "01111100001000010111100011000101111", 'I': "01110001000010000100001000010001110", 'M': "10001110111010110101100011000110001", 'P': "11110100011000111110100001000010000", '-': "00000000000000011111000000000000000",
}
var pdfToken = regexp.MustCompile(`\(([A-Z]{3}-[0-9A-Z]{5})\) Tj`)

// RenderAsset produces a real PNG with bitmap text or a minimal text PDF.
func RenderAsset(format, token string) ([]byte, error) {
	switch format {
	case "png":
		img := image.NewGray(image.Rect(0, 0, len(token)*24+8, 36))
		for i := range img.Pix {
			img.Pix[i] = 255
		}
		for i, r := range token {
			glyph, ok := glyphs[r]
			if !ok {
				return nil, fmt.Errorf("unsupported glyph %c", r)
			}
			for n, pixel := range glyph {
				if pixel != '1' {
					continue
				}
				for y := range 4 {
					for x := range 4 {
						img.SetGray(4+i*24+n%5*4+x, 4+n/5*4+y, color.Gray{})
					}
				}
			}
		}
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	case "pdf":
		return renderPDF(token), nil
	default:
		return nil, fmt.Errorf("unknown asset format %s", format)
	}
}

// AssetToken reads the rendered glyphs or PDF text; it has no item-side lookup table.
func AssetToken(format string, data []byte) (string, error) {
	if format == "pdf" {
		match := pdfToken.FindSubmatch(data)
		if len(match) != 2 {
			return "", fmt.Errorf("missing PDF token")
		}
		return string(match[1]), nil
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	var token strings.Builder
	for i := 0; i < (img.Bounds().Dx()-8)/24; i++ {
		var bits strings.Builder
		for n := range 35 {
			r, _, _, _ := img.At(4+i*24+n%5*4, 4+n/5*4).RGBA()
			if r < 32768 {
				bits.WriteByte('1')
			} else {
				bits.WriteByte('0')
			}
		}
		found := false
		for r, glyph := range glyphs {
			if glyph == bits.String() {
				token.WriteRune(r)
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("unknown rendered glyph")
		}
	}
	return token.String(), nil
}
func renderPDF(token string) []byte {
	stream := "BT /F1 24 Tf 40 100 Td (" + token + ") Tj ET\n"
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 150] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream)}
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, 0, len(objects))
	for i, o := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	start := b.Len()
	fmt.Fprintf(&b, "xref\n0 6\n0000000000 65535 f \n")
	for _, offset := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", start)
	return []byte(b.String())
}
