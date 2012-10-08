// ◄◄◄ fpimage.go ►►►
// Copyright © 2012 Jason Summers

package fpresize

import "image"
import "image/color"

// FPImage is a custom image type, which implements the standard image.Image interface.
// Its internal structure is designed to be similar to Go's standard image structs.
// 
// FPImage's pixels use unassociated alpha, and are presumably in a gamma-corrected
// colorspace (usually sRGB).
type FPImage struct {
	// A slice containing all samples. 4 consecutive floating point samples
	// (R G B A) make a pixel.
	Pix    []float32
	Stride int
	Rect   image.Rectangle
}

const maxImagePixels = 536870911 // ((2^31)-1)/4

// FPColor is a custom color type, used by the FPImage type.
// It implements the color.Color interface.
type FPColor struct {
	R, G, B, A float32
}

// Converts a floating point sample to a uint16 sample.
// Clamps to [0..maxVal].
// Returns a uint16 in a uint32.
func scaleFloatSampleToUint16(s float32, maxVal uint32) uint32 {
	var x uint32

	if s <= 0.0 {
		return 0
	}
	x = uint32(s*65535.0 + 0.5)
	if x > maxVal {
		x = maxVal
	}
	return x
}

// "Color can convert itself to alpha-premultiplied 16-bits per channel RGBA."
// (Method required by the color.Color interface)
func (fpc FPColor) RGBA() (r, g, b, a uint32) {
	a = scaleFloatSampleToUint16(fpc.A, 65535)
	r = scaleFloatSampleToUint16(fpc.R*fpc.A, a)
	g = scaleFloatSampleToUint16(fpc.G*fpc.A, a)
	b = scaleFloatSampleToUint16(fpc.B*fpc.A, a)
	return
}

// fpModel implements color.Model interface
type fpModel struct {
}

func convertColorToFPColor(c color.Color) FPColor {
	var fpc FPColor
	var fpc1 FPColor
	var ok bool
	var r, g, b, a uint32

	// If c is already an FPColor, just return it.
	fpc1, ok = c.(FPColor)
	if ok {
		return fpc1
	}

	r, g, b, a = c.RGBA()
	if a > 0 {
		fpc.R = float32(r) / 65535.0
		fpc.G = float32(g) / 65535.0
		fpc.B = float32(b) / 65535.0
		fpc.A = float32(a) / 65535.0
		if a < 65535 {
			// Convert from premultiplied alpha
			fpc.R /= fpc.A
			fpc.G /= fpc.A
			fpc.B /= fpc.A
		}
	}
	return fpc
}

// "Model can convert any Color to one from its own color model."
func (m *fpModel) Convert(c color.Color) color.Color {
	return convertColorToFPColor(c)
}

// (Method required by the image.Image interface)
func (fp *FPImage) ColorModel() color.Model {
	var fpm fpModel
	return &fpm
}

// (Method required by the image.Image interface)
func (fp *FPImage) Bounds() (r image.Rectangle) {
	return fp.Rect
}

// (Method required by the image.Image interface)
func (fpi *FPImage) At(x, y int) color.Color {
	var fpc FPColor
	x -= fpi.Rect.Min.X
	y -= fpi.Rect.Min.Y
	fpc.R = fpi.Pix[y*fpi.Stride+x*4+0]
	fpc.G = fpi.Pix[y*fpi.Stride+x*4+1]
	fpc.B = fpi.Pix[y*fpi.Stride+x*4+2]
	fpc.A = fpi.Pix[y*fpi.Stride+x*4+3]
	return &fpc
}

// (Method required by the image/draw.Image interface)
func (fpi *FPImage) Set(x, y int, c color.Color) {
	var fpc FPColor

	if !(image.Point{x, y}.In(fpi.Rect)) {
		return
	}

	fpc = convertColorToFPColor(c)
	x -= fpi.Rect.Min.X
	y -= fpi.Rect.Min.Y
	fpi.Pix[y*fpi.Stride+x*4+0] = fpc.R
	fpi.Pix[y*fpi.Stride+x*4+1] = fpc.G
	fpi.Pix[y*fpi.Stride+x*4+2] = fpc.B
	fpi.Pix[y*fpi.Stride+x*4+3] = fpc.A
}
