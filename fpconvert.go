// ◄◄◄ fpconvert.go ►►►
// Copyright © 2012 Jason Summers

// Functions converting to or from our FPImage format.

// Most of this code is related to improving speed. It could be much smaller and
// simpler if we didn't care how slow it ran.

package fpresize

import "image"
import "image/color"
import "errors"

func (fp *FPObject) makeInputLUT_16to32() *[65536]float32 {
	if fp.inputCCF == nil {
		return nil
	}
	if (fp.inputCCFFlags & CCFFlagNoCache) != 0 {
		return nil
	}
	if (fp.inputCCFFlags & CCFFlagWholePixels) != 0 {
		return nil
	}
	if fp.srcW*fp.srcH < 16384*fp.numWorkers {
		// Don't bother with a lookup table if the image is very small.
		// It's hard to estimate what the threshold should be, but accuracy is not
		// very important here.
		return nil
	}

	fp.progressMsgf("Creating input color correction lookup table")

	tbl := new([65536]float32)
	for i := 0; i < 65536; i++ {
		tbl[i] = float32(i) / 65535.0
	}
	fp.inputCCF(tbl[:])
	return tbl
}

// Make a lookup table that takes an int from 0 to tablesize-1,
// and gives a uint8 (representing a sample from 0 to 255).
func (fp *FPObject) makeOutputLUT_Xto8(tableSize int) []uint8 {
	var i int

	if fp.inputCCF == nil {
		return nil
	}
	if (fp.outputCCFFlags & CCFFlagNoCache) != 0 {
		return nil
	}
	if (fp.outputCCFFlags & CCFFlagWholePixels) != 0 {
		return nil
	}
	if fp.dstCanvasW*fp.dstCanvasH < 16384 {
		return nil
	}

	fp.progressMsgf("Creating output color correction lookup table")

	var tempTable = make([]float32, tableSize)

	for i = 0; i < tableSize; i++ {
		tempTable[i] = float32(i) / float32(tableSize-1)
	}
	fp.outputCCF(tempTable)

	tbl := make([]uint8, tableSize)
	for i = 0; i < tableSize; i++ {
		tbl[i] = uint8(tempTable[i]*255.0 + 0.5)
	}
	return tbl
}

// Make a lookup table that takes an int from 0 to tablesize-1,
// and gives a float32 (representing a sample from 0.0 to 1.0).
func (fp *FPObject) makeOutputLUT_Xto32(tableSize int) []float32 {
	var i int

	if fp.inputCCF == nil {
		return nil
	}
	if (fp.outputCCFFlags & CCFFlagNoCache) != 0 {
		return nil
	}
	if (fp.outputCCFFlags & CCFFlagWholePixels) != 0 {
		return nil
	}
	if fp.dstCanvasW*fp.dstCanvasH < 16384 {
		return nil
	}

	fp.progressMsgf("Creating output color correction lookup table")

	tbl := make([]float32, tableSize)
	for i = 0; i < tableSize; i++ {
		tbl[i] = float32(i) / float32(tableSize-1)
	}
	fp.outputCCF(tbl)
	return tbl
}

// Data that is constant for all workers.
type srcToFPWorkContext struct {
	inputLUT_16to32 *[65536]float32
	im              *FPImage
	src_AsRGBA      *image.RGBA
}

type srcToFPWorkItem struct {
	j       int
	stopNow bool
}

func (fp *FPObject) convertSrcToFP_row(wc *srcToFPWorkContext, j int) {
	var r, g, b, a uint32
	var srcclr color.Color
	var i int

	for i = 0; i < fp.srcW; i++ {
		// Read a pixel from the source image, into uint16 samples
		if wc.src_AsRGBA != nil {
			r = uint32(wc.src_AsRGBA.Pix[wc.src_AsRGBA.Stride*j+4*i]) * 257
			g = uint32(wc.src_AsRGBA.Pix[wc.src_AsRGBA.Stride*j+4*i+1]) * 257
			b = uint32(wc.src_AsRGBA.Pix[wc.src_AsRGBA.Stride*j+4*i+2]) * 257
			a = uint32(wc.src_AsRGBA.Pix[wc.src_AsRGBA.Stride*j+4*i+3]) * 257
		} else {
			srcclr = fp.srcImage.At(fp.srcBounds.Min.X+i, fp.srcBounds.Min.Y+j)
			r, g, b, a = srcclr.RGBA()
		}

		if a < 65535 {
			fp.hasTransparency = true
		}

		// Identify the slice of samples representing this pixel in the
		// converted image.
		sam := wc.im.Pix[j*wc.im.Stride+4*i : j*wc.im.Stride+4*i+4]

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
			if wc.inputLUT_16to32 != nil {
				// Convert to linear color, using a lookup table.
				sam[0] = wc.inputLUT_16to32[r]
				sam[1] = wc.inputLUT_16to32[g]
				sam[2] = wc.inputLUT_16to32[b]
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

func (fp *FPObject) srcToFPWorker(wc *srcToFPWorkContext, workQueue chan srcToFPWorkItem) {
	for {
		wi := <-workQueue
		if wi.stopNow {
			return
		}

		fp.convertSrcToFP_row(wc, wi.j)
	}
}

// Copies(&converts) from fp.srcImg to the given image.
func (fp *FPObject) convertSrcToFP(im *FPImage) error {
	var i int
	var j int
	var nSamples int
	var wi srcToFPWorkItem

	if int64(fp.srcW)*int64(fp.srcH) > maxImagePixels {
		return errors.New("Source image too large to process")
	}

	wc := new(srcToFPWorkContext)
	wc.im = im

	// If the underlying type of fp.srcImage is RGBA, we can do some performance
	// optimization.
	// TODO: It would be nice if we could optimize YCbCr images in the same way.
	wc.src_AsRGBA, _ = fp.srcImage.(*image.RGBA)

	// TODO: If wc.src_AsRGBA!=nil, we could use a smaller LUT (8to32).
	wc.inputLUT_16to32 = fp.makeInputLUT_16to32()

	fp.progressMsgf("Converting to FPImage format")

	// Allocate the pixel array
	im.Rect.Min.X = 0
	im.Rect.Min.Y = 0
	im.Rect.Max.X = fp.srcW
	im.Rect.Max.Y = fp.srcH
	im.Stride = fp.srcW * 4
	nSamples = im.Stride * fp.srcH
	im.Pix = make([]float32, nSamples)

	workQueue := make(chan srcToFPWorkItem)

	for i = 0; i < fp.numWorkers; i++ {
		go fp.srcToFPWorker(wc, workQueue)
	}

	// Each row is a "work item". Send each row to a worker.
	for j = 0; j < fp.srcH; j++ {
		wi.j = j
		workQueue <- wi
	}

	// Send out a "stop work" order. When all workers have received it, we know
	// that all the work is done.
	wi.stopNow = true
	for i = 0; i < fp.numWorkers; i++ {
		workQueue <- wi
	}

	return nil
}

// Convert from:
//  * linear colorspace
//  * unassociated alpha
// to:
//  * target colorspace
//  * unassociated alpha
func (fp *FPObject) convertFPToFinalFP(im *FPImage) {
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

// src is floating point, linear colorspace, unassociated alpha
// dst is uint8, target colorspace, unassociated alpha
// It's okay to modify src's pixels; it's about to be thrown away.
func (fp *FPObject) convertFPToNRGBA_internal(src *FPImage, dstPix []uint8, dstStride int) {
	var i, j, k int

	// This table size is optimized for sRGB. The sRGB curve's slope for
	// the darkest colors (the ones we're most concerned about) is 12.92,
	// so our table needs to have around 256*12.92 or more entries to ensure
	// that it includes every possible color value. A size of 255*12.92*3+1 =
	// 9885 improves precision, and makes the dark colors almost always
	// round correctly.
	outputLUT_Xto8_Size := 9885
	outputLUT_Xto8 := fp.makeOutputLUT_Xto8(outputLUT_Xto8_Size)

	if fp.outputCCF == nil {
		fp.progressMsgf("Converting to NRGBA format")
	} else {
		fp.progressMsgf("Converting to target colorspace, and NRGBA format")
	}

	for j = 0; j < (src.Rect.Max.Y - src.Rect.Min.Y); j++ {
		for i = 0; i < (src.Rect.Max.X - src.Rect.Min.X); i++ {
			srcSam := src.Pix[j*src.Stride+i*4 : j*src.Stride+i*4+4]
			dstSam := dstPix[j*dstStride+i*4 : j*dstStride+i*4+4]

			// Set the alpha sample
			if !fp.hasTransparency {
				dstSam[3] = 255
			} else {
				dstSam[3] = uint8(srcSam[3]*255.0 + 0.5)
			}

			// Do colorspace conversion if needed.
			if fp.outputCCF != nil && dstSam[3] > 0 {
				if outputLUT_Xto8 != nil {
					// Do colorspace conversion using a lookup table.
					for k = 0; k < 3; k++ {
						dstSam[k] = outputLUT_Xto8[int(srcSam[k]*float32(outputLUT_Xto8_Size-1)+0.5)]
					}
					continue
				} else {
					// Do colorspace conversion the slow way.
					fp.outputCCF(srcSam[0:3])
				}
			}

			// Set the non-alpha samples (if we didn't use a lookup table).
			for k = 0; k < 3; k++ {
				dstSam[k] = uint8(srcSam[k]*255.0 + 0.5)
			}
		}
	}
}

func (fp *FPObject) convertFPToNRGBA(src *FPImage) (dst *image.NRGBA) {
	dst = image.NewNRGBA(src.Bounds())
	fp.convertFPToNRGBA_internal(src, dst.Pix, dst.Stride)
	return
}

func (fp *FPObject) convertFPToRGBA_internal(src *FPImage) (dst *image.RGBA) {
	var i, j, k int

	dst = image.NewRGBA(src.Bounds())

	// Because we still need to convert to associated alpha after doing color conversion,
	// the lookup table should return high-precision numbers -- uint8 is not enough.
	outputLUT_Xto32_Size := 9885
	outputLUT_Xto32 := fp.makeOutputLUT_Xto32(outputLUT_Xto32_Size)

	if fp.outputCCF == nil {
		fp.progressMsgf("Converting to RGBA format")
	} else {
		fp.progressMsgf("Converting to target colorspace, and RGBA format")
	}

	for j = 0; j < (src.Rect.Max.Y - src.Rect.Min.Y); j++ {
		for i = 0; i < (src.Rect.Max.X - src.Rect.Min.X); i++ {
			srcSam := src.Pix[j*src.Stride+i*4 : j*src.Stride+i*4+4]
			dstSam := dst.Pix[j*dst.Stride+i*4 : j*dst.Stride+i*4+4]

			// Set the alpha sample
			if !fp.hasTransparency {
				dstSam[3] = 255
			} else {
				dstSam[3] = uint8(srcSam[3]*255.0 + 0.5)
			}

			// Do colorspace conversion if needed.
			if fp.outputCCF != nil && dstSam[3] > 0 {
				if outputLUT_Xto32 != nil {
					// Do colorspace conversion using a lookup table.
					for k = 0; k < 3; k++ {
						srcSam[k] = outputLUT_Xto32[int(srcSam[k]*float32(outputLUT_Xto32_Size-1)+0.5)]
					}
				} else {
					// Do colorspace conversion the slow way.
					fp.outputCCF(srcSam[0:3])
				}
			}

			// Set the non-alpha samples (converting to associated alpha)
			for k = 0; k < 3; k++ {
				dstSam[k] = uint8((srcSam[k]*srcSam[3])*255.0 + 0.5)
			}
		}
	}
	return
}

func (fp *FPObject) convertFPToRGBA(src *FPImage) (dst *image.RGBA) {
	if fp.hasTransparency {
		return fp.convertFPToRGBA_internal(src)
	}

	// If the image has no transparency, use convertFPToNRGBA_internal,
	// which is usually somewhat faster.
	dst = image.NewRGBA(src.Bounds())
	fp.convertFPToNRGBA_internal(src, dst.Pix, dst.Stride)
	return
}

func (fp *FPObject) convertFPToNRGBA64(src *FPImage) (dst *image.NRGBA64) {
	var i, j, k int
	var dstSam [4]uint16

	dst = image.NewNRGBA64(src.Bounds())

	if fp.outputCCF == nil {
		fp.progressMsgf("Converting to NRGBA64 format")
	} else {
		fp.progressMsgf("Converting to target colorspace, and NRGBA64 format")
	}

	for j = 0; j < (src.Rect.Max.Y - src.Rect.Min.Y); j++ {
		for i = 0; i < (src.Rect.Max.X - src.Rect.Min.X); i++ {
			srcSam := src.Pix[j*src.Stride+i*4 : j*src.Stride+i*4+4]

			// Set the alpha sample
			if !fp.hasTransparency {
				dstSam[3] = 65535
			} else {
				dstSam[3] = uint16(srcSam[3]*65535.0 + 0.5)
			}

			// Do colorspace conversion if needed.
			if fp.outputCCF != nil && dstSam[3] > 0 {
				fp.outputCCF(srcSam[0:3])
			}

			// Calculate the non-alpha samples.
			for k = 0; k < 3; k++ {
				dstSam[k] = uint16(srcSam[k]*65535.0 + 0.5)
			}

			// Convert all samples to NRGBA format
			dstPixelData := dst.Pix[j*dst.Stride+i*8 : j*dst.Stride+i*8+8]
			for k = 0; k < 4; k++ {
				dstPixelData[k*2] = uint8(dstSam[k] >> 8)
				dstPixelData[k*2+1] = uint8(dstSam[k] & 0xff)
			}
		}
	}
	return
}

// TODO: If we are not going to use lookup tables to speed up color correction,
// this function and convertFPToNRGBA64 could be merged.
func (fp *FPObject) convertFPToRGBA64(src *FPImage) (dst *image.RGBA64) {
	var i, j, k int
	var dstSam [4]uint16

	dst = image.NewRGBA64(src.Bounds())

	if fp.outputCCF == nil {
		fp.progressMsgf("Converting to RGBA64 format")
	} else {
		fp.progressMsgf("Converting to target colorspace, and RGBA64 format")
	}

	for j = 0; j < (src.Rect.Max.Y - src.Rect.Min.Y); j++ {
		for i = 0; i < (src.Rect.Max.X - src.Rect.Min.X); i++ {
			srcSam := src.Pix[j*src.Stride+i*4 : j*src.Stride+i*4+4]

			// Set the alpha sample
			if !fp.hasTransparency {
				dstSam[3] = 65535
			} else {
				dstSam[3] = uint16(srcSam[3]*65535.0 + 0.5)
			}

			// Do colorspace conversion if needed.
			if fp.outputCCF != nil && dstSam[3] > 0 {
				fp.outputCCF(srcSam[0:3])
			}

			// Calculate the non-alpha samples.
			for k = 0; k < 3; k++ {
				dstSam[k] = uint16((srcSam[k]*srcSam[3])*65535.0 + 0.5)
			}

			// Convert all samples to NRGBA format
			dstPixelData := dst.Pix[j*dst.Stride+i*8 : j*dst.Stride+i*8+8]
			for k = 0; k < 4; k++ {
				dstPixelData[k*2] = uint8(dstSam[k] >> 8)
				dstPixelData[k*2+1] = uint8(dstSam[k] & 0xff)
			}
		}
	}
	return
}
