// ◄◄◄ fpconvert.go ►►►
// Copyright © 2012 Jason Summers

// Functions converting to or from our FPImage format.

// Most of this code is related to improving speed. It could be much smaller and
// simpler if we didn't care how slow it ran.

package fpresize

import "image"
import "image/color"
import "errors"

func (fp *FPObject) makeInputCCLookupTable() {
	if fp.inputCCF == nil {
		return
	}
	if (fp.inputCCFFlags & CCFFlagNoCache) != 0 {
		return
	}
	if (fp.inputCCFFlags & CCFFlagWholePixels) != 0 {
		return
	}
	if fp.srcW*fp.srcH < 16384 {
		// Don't bother with a lookup table if the image is very small.
		// It's hard to estimate what the threshold should be, but accuracy is not
		// very important here.
		return
	}

	fp.progressMsgf("Creating input color correction lookup table")

	fp.inputCCLookupTable16 = new([65536]float32)
	for i := 0; i < 65536; i++ {
		fp.inputCCLookupTable16[i] = float32(i) / 65535.0
	}
	fp.inputCCF(fp.inputCCLookupTable16[:])
}

// Make a lookup table that takes an int from 0 to tablesize-1,
// and gives a uint8 (representing a sample from 0 to 255).
func (fp *FPObject) makeOutputCCTableX_8(tableSize int) {
	var i int

	if fp.inputCCF == nil {
		return
	}
	if (fp.outputCCFFlags & CCFFlagNoCache) != 0 {
		return
	}
	if (fp.outputCCFFlags & CCFFlagWholePixels) != 0 {
		return
	}
	if fp.dstW*fp.dstH < 16384 {
		return
	}

	fp.progressMsgf("Creating output color correction lookup table")

	fp.outputCCTableX_8Size = tableSize

	var tempTable = make([]float32, fp.outputCCTableX_8Size)

	for i = 0; i < fp.outputCCTableX_8Size; i++ {
		tempTable[i] = float32(i) / float32(fp.outputCCTableX_8Size-1)
	}
	fp.outputCCF(tempTable)

	fp.outputCCTableX_8 = make([]uint8, fp.outputCCTableX_8Size)
	for i = 0; i < fp.outputCCTableX_8Size; i++ {
		fp.outputCCTableX_8[i] = uint8(tempTable[i]*255.0 + 0.5)
	}
}

// Make a lookup table that takes an int from 0 to tablesize-1,
// and gives a float32 (representing a sample from 0.0 to 1.0).
func (fp *FPObject) makeOutputCCTableX_32(tableSize int) {
	var i int

	if fp.inputCCF == nil {
		return
	}
	if (fp.outputCCFFlags & CCFFlagNoCache) != 0 {
		return
	}
	if (fp.outputCCFFlags & CCFFlagWholePixels) != 0 {
		return
	}
	if fp.dstW*fp.dstH < 16384 {
		return
	}

	fp.progressMsgf("Creating output color correction lookup table")

	fp.outputCCTableX_32Size = tableSize

	fp.outputCCTableX_32 = make([]float32, fp.outputCCTableX_32Size)
	for i = 0; i < fp.outputCCTableX_32Size; i++ {
		fp.outputCCTableX_32[i] = float32(i) / float32(fp.outputCCTableX_32Size-1)
	}
	fp.outputCCF(fp.outputCCTableX_32)
}

// Copies(&converts) from fp.srcImg to the given image.
func (fp *FPObject) copySrcToFPImage(im *FPImage) error {
	var i, j int
	var nSamples int
	var r, g, b, a uint32
	var srcclr color.Color

	if int64(fp.srcW)*int64(fp.srcH) > maxImagePixels {
		return errors.New("Source image too large to process")
	}

	fp.makeInputCCLookupTable()

	fp.progressMsgf("Converting to FPImage format")

	// Allocate the pixel array
	im.Rect.Min.X = 0
	im.Rect.Min.Y = 0
	im.Rect.Max.X = fp.srcW
	im.Rect.Max.Y = fp.srcH
	im.Stride = fp.srcW * 4
	nSamples = im.Stride * fp.srcH
	im.Pix = make([]float32, nSamples)

	// If the underlying type of fp.srcImage is RGBA, we can do some performance
	// optimization.
	src_as_RGBA, _ := fp.srcImage.(*image.RGBA)

	for j = 0; j < fp.srcH; j++ {
		for i = 0; i < fp.srcW; i++ {
			// Read a pixel from the source image, into uint16 samples
			if src_as_RGBA != nil {
				r = uint32(src_as_RGBA.Pix[src_as_RGBA.Stride*j+4*i]) * 257
				g = uint32(src_as_RGBA.Pix[src_as_RGBA.Stride*j+4*i+1]) * 257
				b = uint32(src_as_RGBA.Pix[src_as_RGBA.Stride*j+4*i+2]) * 257
				a = uint32(src_as_RGBA.Pix[src_as_RGBA.Stride*j+4*i+3]) * 257
			} else {
				srcclr = fp.srcImage.At(fp.srcBounds.Min.X+i, fp.srcBounds.Min.Y+j)
				r, g, b, a = srcclr.RGBA()
			}

			if a < 65535 {
				fp.hasTransparency = true
			}

			// Identify the slice of samples representing this pixel in the
			// converted image.
			sam := im.Pix[j*im.Stride+4*i : j*im.Stride+4*i+4]

			// Choose from among several methods of converting the pixel to our
			// desired format.
			if a == 0 {
				// Handle fully-transparent pixels quickly.
				// Color correction is irrelevant here.
				// Nothing to do: the samples will have been initialized to 0.0,
				// which is what we want.
			} else if fp.inputCCF == nil {
				// No color correction; just convert from uint16(0 ... 65535) to float(0.0 ... 1.0)
				sam[0] = float32(r) / 65535.0
				sam[1] = float32(g) / 65535.0
				sam[2] = float32(b) / 65535.0
				sam[3] = float32(a) / 65535.0
			} else if a == 65535 {
				// Fast path for fully-opaque pixels.
				if fp.inputCCLookupTable16 != nil {
					// Convert to linear color, using a lookup table.
					sam[0] = fp.inputCCLookupTable16[r]
					sam[1] = fp.inputCCLookupTable16[g]
					sam[2] = fp.inputCCLookupTable16[b]
				} else {
					// Convert to linear color, without a lookup table.
					sam[0] = float32(r) / 65535.0
					sam[1] = float32(g) / 65535.0
					sam[2] = float32(b) / 65535.0
					fp.inputCCF(sam[0:3])
				}
				sam[3] = 1.0
			} else {
				// Partial transparency, with color correction.
				// Convert to floating point.
				sam[0] = float32(r) / 65535.0
				sam[1] = float32(g) / 65535.0
				sam[2] = float32(b) / 65535.0
				sam[3] = float32(a) / 65535.0
				// Convert to unassociated alpha, so that we can do color conversion.
				sam[0] /= sam[3]
				sam[1] /= sam[3]
				sam[2] /= sam[3]
				// Convert to linear color.
				// (inputCCLookupTable16 could be used, but wouldn't be as accurate,
				// because the colors won't appear in it exactly.)
				fp.inputCCF(sam[0:3])
				// Convert back to associated alpha.
				sam[0] *= sam[3]
				sam[1] *= sam[3]
				sam[2] *= sam[3]
			}
		}
	}
	return nil
}

// Convert from:
//  * linear colorspace
//  * unassociated alpha
// to:
//  * target colorspace
//  * unassociated alpha
func (fp *FPObject) convertDstFPImage(im *FPImage) {
	var i, j int

	if fp.outputCCF == nil {
		return
	}

	fp.progressMsgf("Converting to target colorspace")

	for j = 0; j < (im.Rect.Max.Y - im.Rect.Min.Y); j++ {
		for i = 0; i < (im.Rect.Max.X - im.Rect.Min.X); i++ {
			// Identify the slice of samples representing the pixel we're updating.
			sam := im.Pix[j*im.Stride+i*4 : j*im.Stride+i*4+4]

			if sam[3] <= 0.0 {
				// A fully transparent pixel (nothing to do)
				continue
			}

			// Convert to target colorspace
			fp.outputCCF(sam[0:3])
		}
	}
}

// im1 is floating point, linear colorspace, unassociated alpha
// im2 will be uint8, target colorspace, unassociated alpha
// It's okay to modify im1's pixels; it's about to be thrown away.
func (fp *FPObject) convertDstFPImageToNRGBA(im1 *FPImage) *image.NRGBA {
	var i, j, k int

	im2 := image.NewNRGBA(im1.Bounds())

	// TODO: Figure out a suitable size for the lookup table.
	fp.makeOutputCCTableX_8(10000)

	if fp.outputCCF == nil {
		fp.progressMsgf("Converting to NRGBA format")
	} else {
		fp.progressMsgf("Converting to target colorspace, and NRGBA format")
	}

	for j = 0; j < (im1.Rect.Max.Y - im1.Rect.Min.Y); j++ {
		for i = 0; i < (im1.Rect.Max.X - im1.Rect.Min.X); i++ {
			sam1 := im1.Pix[j*im1.Stride+i*4 : j*im1.Stride+i*4+4]
			sam2 := im2.Pix[j*im2.Stride+i*4 : j*im2.Stride+i*4+4]

			// Set the alpha sample
			if !fp.hasTransparency {
				sam2[3] = 255
			} else {
				sam2[3] = uint8(sam1[3]*255.0 + 0.5)
			}

			// Do colorspace conversion if needed.
			if fp.outputCCF != nil {
				if fp.outputCCTableX_8 != nil {
					// Do colorspace conversion using a lookup table.
					for k = 0; k < 3; k++ {
						sam2[k] = fp.outputCCTableX_8[int(sam1[k]*float32(fp.outputCCTableX_8Size-1)+0.5)]
					}
					continue
				} else {
					// Do colorspace conversion the slow way.
					fp.outputCCF(sam1[0:3])
				}
			}

			// Set the non-alpha samples.
			for k = 0; k < 3; k++ {
				sam2[k] = uint8(sam1[k]*255.0 + 0.5)
			}
		}
	}

	return im2
}

func (fp *FPObject) convertDstFPImageToRGBA(im1 *FPImage) *image.RGBA {
	var i, j, k int

	im2 := image.NewRGBA(im1.Bounds())

	// Because we still need to convert to associated alpha after doing color conversion,
	// the lookup table should return high-precision numbers -- uint8 is not enough.
	fp.makeOutputCCTableX_32(10000)

	if fp.outputCCF == nil {
		fp.progressMsgf("Converting to RGBA format")
	} else {
		fp.progressMsgf("Converting to target colorspace, and RGBA format")
	}

	for j = 0; j < (im1.Rect.Max.Y - im1.Rect.Min.Y); j++ {
		for i = 0; i < (im1.Rect.Max.X - im1.Rect.Min.X); i++ {
			sam1 := im1.Pix[j*im1.Stride+i*4 : j*im1.Stride+i*4+4]
			sam2 := im2.Pix[j*im2.Stride+i*4 : j*im2.Stride+i*4+4]

			// Set the alpha sample
			if !fp.hasTransparency {
				sam2[3] = 255
			} else {
				sam2[3] = uint8(sam1[3]*255.0 + 0.5)
			}

			// Do colorspace conversion if needed.
			if fp.outputCCF != nil {
				if fp.outputCCTableX_32 != nil {
					// Do colorspace conversion using a lookup table.
					for k = 0; k < 3; k++ {
						sam1[k] = fp.outputCCTableX_32[int(sam1[k]*float32(fp.outputCCTableX_32Size-1)+0.5)]
					}
				} else {
					// Do colorspace conversion the slow way.
					fp.outputCCF(sam1[0:3])
				}
			}

			// Set the non-alpha samples (converting to associated alpha)
			for k = 0; k < 3; k++ {
				sam2[k] = uint8((sam1[k]*sam1[3])*255.0 + 0.5)
			}
		}
	}

	return im2
}

func (fpi *FPImage) copyToNRGBA64() *image.NRGBA64 {
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
