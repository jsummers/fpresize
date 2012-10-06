// ◄◄◄ fpconvert.go ►►►
// Copyright © 2012 Jason Summers

// Functions converting to or from our FPImage format.

// Most of this code is related to improving speed. It could be much smaller and
// simpler if we didn't care how slow it ran.

package fpresize

import "image"
import "errors"

func (fp *FPObject) makeInputLUT_Xto32(tableSize int) []float32 {
	if fp.inputCCF == nil {
		return nil
	}
	if (fp.inputCCFFlags & CCFFlagNoCache) != 0 {
		return nil
	}
	if (fp.inputCCFFlags & CCFFlagWholePixels) != 0 {
		return nil
	}
	if fp.srcW*fp.srcH < (tableSize/4)*fp.numWorkers {
		// Don't bother with a lookup table if the image is very small.
		// It's hard to estimate what the threshold should be, but accuracy is not
		// very important here.
		return nil
	}

	fp.progressMsgf("Creating input color correction lookup table")

	tbl := make([]float32, tableSize)
	for i := 0; i < tableSize; i++ {
		tbl[i] = float32(i) / float32(tableSize-1)
	}
	fp.inputCCF(tbl)
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
	inputLUT_8to32  []float32
	inputLUT_16to32 []float32
	dst             *FPImage
	src_AsRGBA      *image.RGBA
	src_AsNRGBA     *image.NRGBA
}

type srcToFPWorkItem struct {
	j       int
	stopNow bool
}

// Convert row j from fp.srcImage (or wc.src_AsRGBA if available)
// to wc.dst.
func (fp *FPObject) convertSrcToFP_row(wc *srcToFPWorkContext, j int) {
	var srcSam16 [4]uint32 // Source RGBA samples (uint16 stored in uint32)
	var k int

	for i := 0; i < fp.srcW; i++ {
		// Read a pixel from the source image, into uint16 samples
		if wc.src_AsRGBA != nil {
			for k = 0; k < 4; k++ {
				srcSam16[k] = uint32(wc.src_AsRGBA.Pix[wc.src_AsRGBA.Stride*j+4*i+k]) * 257
			}
		} else {
			srcclr := fp.srcImage.At(fp.srcBounds.Min.X+i, fp.srcBounds.Min.Y+j)
			srcSam16[0], srcSam16[1], srcSam16[2], srcSam16[3] = srcclr.RGBA()
		}

		if srcSam16[3] < 65535 {
			fp.hasTransparency = true
		}

		// Identify the slice of samples representing this pixel in the
		// converted image.
		dstSam := wc.dst.Pix[j*wc.dst.Stride+4*i : j*wc.dst.Stride+4*i+4]

		// Choose from among several methods of converting the pixel to our
		// desired format.
		if srcSam16[3] == 0 {
			// Handle fully-transparent pixels quickly.
			// Color correction is irrelevant here.
			// Nothing to do: the samples will have been initialized to 0.0,
			// which is what we want.
		} else if fp.inputCCF == nil {
			// No color correction; just convert from uint16(0 ... 65535) to float(0.0 ... 1.0)
			for k = 0; k < 4; k++ {
				dstSam[k] = float32(srcSam16[k]) / 65535.0
			}
		} else if srcSam16[3] == 65535 {
			// Fast path for fully-opaque pixels.
			if wc.inputLUT_16to32 != nil {
				// Convert to linear color, using a lookup table.
				for k = 0; k < 3; k++ {
					dstSam[k] = wc.inputLUT_16to32[srcSam16[k]]
				}
			} else {
				// Convert to linear color, without a lookup table.
				for k = 0; k < 3; k++ {
					dstSam[k] = float32(srcSam16[k]) / 65535.0
				}
				fp.inputCCF(dstSam[0:3])
			}
			dstSam[3] = 1.0
		} else {
			// Partial transparency, with color correction.
			// Convert to floating point,
			// and to unassociated alpha, so that we can do color conversion.
			dstSam[3] = float32(srcSam16[3]) // Leave at (0...65535) for the next loop
			for k = 0; k < 3; k++ {
				dstSam[k] = float32(srcSam16[k]) / dstSam[3]
			}
			dstSam[3] /= 65535.0

			// Convert to linear color.
			// (inputLUT_16to32 could be used, but wouldn't be as accurate,
			// because the colors won't appear in it exactly.)
			fp.inputCCF(dstSam[0:3])
			// Convert back to associated alpha.
			for k = 0; k < 3; k++ {
				dstSam[k] *= dstSam[3]
			}
		}
	}
}

// Convert row j from wc.src_AsNRGBA to wc.dst.
// This is an optimized version of convertSrcToFP_row().
func (fp *FPObject) convertSrcToFP_row_NRGBA(wc *srcToFPWorkContext, j int) {
	var k int

	for i := 0; i < fp.srcW; i++ {
		var srcSam8 []uint8
		srcSam8 = wc.src_AsNRGBA.Pix[wc.src_AsNRGBA.Stride*j+4*i : wc.src_AsNRGBA.Stride*j+4*i+4]

		if srcSam8[3] < 255 {
			fp.hasTransparency = true

			if srcSam8[3] == 0 {
				// No need to do anything if the pixel is fully transparent.
				continue
			}
		}

		// Identify the slice of samples representing this pixel in the
		// converted image.
		dstSam := wc.dst.Pix[j*wc.dst.Stride+4*i : j*wc.dst.Stride+4*i+4]

		// Convert to floating point
		for k = 0; k < 4; k++ {
			dstSam[k] = float32(srcSam8[k]) / 255.0
		}

		// Do color correction, if necessary
		if fp.inputCCF != nil {
			if wc.inputLUT_8to32 != nil {
				// Convert to linear color, using a lookup table.
				for k = 0; k < 3; k++ {
					dstSam[k] = wc.inputLUT_8to32[srcSam8[k]]
				}
			} else {
				// Convert to linear color, without a lookup table.
				fp.inputCCF(dstSam[0:3])
			}
		}

		// Convert to associated alpha, if not fully opaque
		if srcSam8[3] != 255 {
			for k = 0; k < 3; k++ {
				dstSam[k] *= dstSam[3]
			}
		}
	}
}

func (fp *FPObject) srcToFPWorker(wc *srcToFPWorkContext, workQueue chan srcToFPWorkItem) {
	for {
		wi := <-workQueue
		if wi.stopNow {
			return
		}

		if wc.src_AsNRGBA != nil {
			fp.convertSrcToFP_row_NRGBA(wc, wi.j)
		} else {
			fp.convertSrcToFP_row(wc, wi.j)
		}
	}
}

// Copies(&converts) from fp.srcImg to the given image.
func (fp *FPObject) convertSrcToFP(dst *FPImage) error {
	var i int
	var j int
	var nSamples int
	var wi srcToFPWorkItem

	if int64(fp.srcW)*int64(fp.srcH) > maxImagePixels {
		return errors.New("Source image too large to process")
	}

	wc := new(srcToFPWorkContext)
	wc.dst = dst

	// If the underlying type of fp.srcImage is RGBA or NRGBA, we can do some
	// performance optimization.
	// TODO: It would be nice if we could optimize YCbCr images in the same way.
	wc.src_AsRGBA, _ = fp.srcImage.(*image.RGBA)
	wc.src_AsNRGBA, _ = fp.srcImage.(*image.NRGBA)

	if wc.src_AsNRGBA != nil {
		wc.inputLUT_8to32 = fp.makeInputLUT_Xto32(256)
	} else {
		wc.inputLUT_16to32 = fp.makeInputLUT_Xto32(65536)
	}

	fp.progressMsgf("Converting to FPImage format")

	// Allocate the pixel array
	dst.Rect.Min.X = 0
	dst.Rect.Min.Y = 0
	dst.Rect.Max.X = fp.srcW
	dst.Rect.Max.Y = fp.srcH
	dst.Stride = fp.srcW * 4
	nSamples = dst.Stride * fp.srcH
	dst.Pix = make([]float32, nSamples)

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

// Convert in-place from:
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
