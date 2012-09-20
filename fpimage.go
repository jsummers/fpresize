// ◄◄◄ fpimage.go ►►►
// Copyright © 2012 Jason Summers

package fpresize

import "image"
import "image/color"

// FPImage is a custom image type, which implements the standard image.Image interface.
// Its internal structure is designed to be similar to Go's standard image structs.
// 
// FPImage's pixels normally use unassociated alpha, and are in a nonlinear colorspace
// (presumably sRGB).
type FPImage struct {
	// A slice containing all samples. 4 consecutive floating point samples (R G B A)
	// make a pixel.
	Pix    []float32
	Stride int
	Rect   image.Rectangle
}

const maxImagePixels = 536870911 // ((2^31)-1)/4

// fpColor implements the color.Color interface
type fpColor struct {
	sam [4]float32
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

func (fpc *fpColor) RGBA() (r, g, b, a uint32) {
	a = scaleFloatSampleToUint16(fpc.sam[3], 65535)
	r = scaleFloatSampleToUint16(fpc.sam[0]*fpc.sam[3], a)
	g = scaleFloatSampleToUint16(fpc.sam[1]*fpc.sam[3], a)
	b = scaleFloatSampleToUint16(fpc.sam[2]*fpc.sam[3], a)
	return
}

// fpModel implements color.Model interface
type fpModel struct {
}

// "Model can convert any Color to one from its own color model."
// TODO: Implement this.
func (m *fpModel) Convert(c color.Color) color.Color {
	var fpc fpColor
	fpc.sam[0] = 1.0
	fpc.sam[1] = 0.0
	fpc.sam[2] = 1.0
	fpc.sam[3] = 1.0
	return &fpc
}

func (fp *FPImage) ColorModel() color.Model {
	var fpm fpModel
	return &fpm
}

func (fp *FPImage) Bounds() (r image.Rectangle) {
	return fp.Rect
}

func (fpi *FPImage) At(x, y int) color.Color {
	var fpc fpColor
	fpc.sam[0] = fpi.Pix[y*fpi.Stride+x*4+0]
	fpc.sam[1] = fpi.Pix[y*fpi.Stride+x*4+1]
	fpc.sam[2] = fpi.Pix[y*fpi.Stride+x*4+2]
	fpc.sam[3] = fpi.Pix[y*fpi.Stride+x*4+3]
	return &fpc
}

// CopyToNRGBA is a utility function, useful when writing PNG images,
// to avoid having to convert to associated alpha and then back to
// unassociated alpha.
func (fpi *FPImage) CopyToNRGBA() *image.NRGBA {
	dst := image.NewNRGBA(fpi.Bounds())
	for j := 0; j < (fpi.Rect.Max.Y - fpi.Rect.Min.Y); j++ {
		for i := 0; i < (fpi.Rect.Max.X-fpi.Rect.Min.X)*4; i++ {
			dst.Pix[j*dst.Stride+i] = uint8(fpi.Pix[j*fpi.Stride+i]*255.0 + 0.5)
		}
	}
	return dst
}

func (fpi *FPImage) CopyToNRGBA64() *image.NRGBA64 {
	var n uint32
	dst := image.NewNRGBA64(fpi.Bounds())
	for j := 0; j < (fpi.Rect.Max.Y - fpi.Rect.Min.Y); j++ {
		for i := 0; i < (fpi.Rect.Max.X-fpi.Rect.Min.X)*4; i++ {
			n = uint32(fpi.Pix[j*fpi.Stride+i]*65535.0 + 0.5)
			dst.Pix[j*dst.Stride+i*2+0] = uint8(n >> 8)
			dst.Pix[j*dst.Stride+i*2+1] = uint8(n)
		}
	}
	return dst
}
