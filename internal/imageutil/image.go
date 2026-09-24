// Package imageutil provides the image operations needed by browser screenshots.
package imageutil

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// Decode returns an independent, zero-origin RGBA image.
func Decode(data []byte) (*image.RGBA, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	dst := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Src)
	return dst, nil
}

// Crop clips the requested area and copies it into a zero-origin image.
func Crop(src *image.RGBA, rect image.Rectangle) (*image.RGBA, error) {
	if src == nil {
		return nil, errors.New("nil image")
	}
	rect = rect.Intersect(src.Bounds())
	if rect.Empty() {
		return nil, errors.New("crop area out of bounds or empty")
	}
	dst := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(dst, dst.Bounds(), src, rect.Min, draw.Src)
	return dst, nil
}

// Resize uses nearest-neighbor sampling, matching screenshot tile scaling.
func Resize(src *image.RGBA, width, height int) (*image.RGBA, error) {
	if src == nil || src.Bounds().Empty() {
		return nil, errors.New("empty image")
	}
	if width <= 0 || height <= 0 {
		return nil, errors.New("invalid image dimensions")
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	b := src.Bounds()
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			dst.SetRGBA(x, y, src.RGBAAt(b.Min.X+x*b.Dx()/width, b.Min.Y+y*b.Dy()/height))
		}
	}
	return dst, nil
}
