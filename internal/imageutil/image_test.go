package imageutil

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestDecodeFormats(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			src.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	for _, format := range []string{"png", "jpeg", "gif"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			var err error
			switch format {
			case "png":
				err = png.Encode(&buf, src)
			case "jpeg":
				err = jpeg.Encode(&buf, src, &jpeg.Options{Quality: 90})
			case "gif":
				err = gif.Encode(&buf, src, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := Decode(buf.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if got.Bounds() != src.Bounds() {
				t.Fatalf("bounds = %v", got.Bounds())
			}
			pixel := got.RGBAAt(2, 1)
			if pixel.R < 250 || pixel.G > 5 || pixel.B > 5 || pixel.A != 255 {
				t.Fatalf("pixel = %v", pixel)
			}
		})
	}
	if _, err := Decode([]byte("invalid")); err == nil {
		t.Fatal("invalid image accepted")
	}
}

func TestCropAndResizeNonZeroOrigin(t *testing.T) {
	src := image.NewRGBA(image.Rect(10, 20, 12, 22))
	src.SetRGBA(10, 20, color.RGBA{R: 255, A: 255})
	src.SetRGBA(11, 20, color.RGBA{G: 255, A: 255})
	src.SetRGBA(10, 21, color.RGBA{B: 255, A: 255})
	src.SetRGBA(11, 21, color.RGBA{R: 128, A: 128})
	got, err := Crop(src, image.Rect(9, 19, 12, 22))
	if err != nil {
		t.Fatal(err)
	}
	if got.Bounds() != image.Rect(0, 0, 2, 2) {
		t.Fatalf("crop bounds = %v", got.Bounds())
	}
	got.SetRGBA(0, 0, color.RGBA{})
	if src.RGBAAt(10, 20).R != 255 {
		t.Fatal("crop aliases source pixels")
	}
	scaled, err := Resize(src, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if scaled.RGBAAt(x, y) != src.RGBAAt(10+x/2, 20+y/2) {
				t.Fatalf("nearest neighbor mismatch at %d,%d", x, y)
			}
		}
	}
	if _, err := Crop(src, image.Rect(0, 0, 1, 1)); err == nil {
		t.Fatal("outside crop accepted")
	}
	if _, err := Resize(src, 0, 2); err == nil {
		t.Fatal("zero width accepted")
	}
}
