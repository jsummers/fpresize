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

// Convert row j from fp.srcImage to wc.dst.
func (fp *FPObject) convertSrcToFP_row(wc *srcToFPWorkContext, j int) {
	var srcSam16 [4]uint32 // Source RGBA samples (uint16 stored in uint32)
	var k int

	for i := 0; i < fp.srcW; i++ {
		// Read a pixel from the source image, into uint16 samples
		srcclr := fp.srcImage.At(fp.srcBounds.Min.X+i, fp.srcBounds.Min.Y+j)
		srcSam16[0], srcSam16[1], srcSam16[2], srcSam16[3] = srcclr.RGBA()

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

		// Do color correction, if necessary
		if fp.inputCCF != nil && wc.inputLUT_8to32 != nil {
			// Convert to linear color, using a lookup table.
			for k = 0; k < 3; k++ {
				dstSam[k] = wc.inputLUT_8to32[srcSam8[k]]
			}
			dstSam[3] = float32(srcSam8[3]) / 255.0
		} else {
			// In all other cases, first read the uncorrected samples into dstSam
			for k = 0; k < 4; k++ {
				dstSam[k] = float32(srcSam8[k]) / 255.0
			}

			if fp.inputCCF != nil {
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

// Convert row j from wc.src_AsRGBA to wc.dst.
// This is an optimized version of convertSrcToFP_row().
func (fp *FPObject) convertSrcToFP_row_RGBA(wc *srcToFPWorkContext, j int) {
	var k int

	for i := 0; i < fp.srcW; i++ {
		var srcSam8 []uint8
		srcSam8 = wc.src_AsRGBA.Pix[wc.src_AsRGBA.Stride*j+4*i : wc.src_AsRGBA.Stride*j+4*i+4]

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

		// If we are going to use a lookup table, do that now.
		if srcSam8[3] == 255 && fp.inputCCF != nil && wc.inputLUT_8to32 != nil {
			for k = 0; k < 3; k++ {
				dstSam[k] = wc.inputLUT_8to32[srcSam8[k]]
			}
			dstSam[3] = 1.0
			continue
		}

		// Otherwise, convert the sample to floating point
		for k = 0; k < 4; k++ {
			dstSam[k] = float32(srcSam8[k]) / 255.0
		}

		if fp.inputCCF == nil {
			// If not doing color correction, we're done.
			continue
		}

		if srcSam8[3] == 255 { // If pixel is opaque...
			// Opaque pixel, with color correction, but no LUT
			fp.inputCCF(dstSam[0:3])
			continue
		}

		// Pixel is partially transparent.

		// Convert to unassociated alpha, so we can do color correction.
		for k = 0; k < 3; k++ {
			dstSam[k] /= dstSam[3]
		}

		// Do color correction.
		// (Don't use a lookup table; it doesn't have enough precision in this case.)
		fp.inputCCF(dstSam[0:3])

		// Convert back to associated alpha
		for k = 0; k < 3; k++ {
			dstSam[k] *= dstSam[3]
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
		} else if wc.src_AsRGBA != nil {
			fp.convertSrcToFP_row_RGBA(wc, wi.j)
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

	if wc.src_AsRGBA != nil || wc.src_AsNRGBA != nil {
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

// Take a row fresh from resizeWidth/resizeHeight
//  * associated alpha, linear colorspace, alpha samples may not be valid
// Convert to 
//  * unassociated alpha, linear colorspace, alpha samples always valid,
//    all samples clamped to [0,1].
//
// It is possible that we will convert to unassociated alpha needlessly, only
// to convert right back to associated alpha. That will happen if color
// correction is disabled, and the final image format uses associated alpha.
// We may optimize for that case in a future version.
func (fp *FPObject) postProcessImage_row(im *FPImage, j int) {
	var k int

	for i := 0; i < (im.Rect.Max.X - im.Rect.Min.X); i++ {
		rp := j*im.Stride + i*4 // index of the Red sample in im.Pix
		ap := rp + 3            // index of the alpha sample

		if !fp.hasTransparency {
			// This image is known to have no transparency. Set alpha to 1,
			// and clamp the other samples to [0,1]
			for k = 0; k < 3; k++ {
				if im.Pix[rp+k] < 0.0 {
					im.Pix[rp+k] = 0.0
				} else if im.Pix[rp+k] > 1.0 {
					im.Pix[rp+k] = 1.0
				}
			}
			im.Pix[ap] = 1.0
			continue
		} else if im.Pix[ap] <= 0.0 {
			// A fully transparent pixel
			for k = 0; k < 4; k++ {
				im.Pix[rp+k] = 0.0
			}
			continue
		}

		// With some filters, it is possible to end up with an alpha value larger
		// than 1. If that happens, it makes a difference whether we clamp the
		// samples to valid values before, or after converting to unassociated alpha.
		// I don't know which is better. The current code converts first, then clamps.

		// Convert to unassociated alpha
		if im.Pix[ap] != 1.0 {
			for k = 0; k < 3; k++ {
				im.Pix[rp+k] /= im.Pix[ap]
			}
		}

		// Clamp to [0,1]
		for k = 0; k < 4; k++ {
			if im.Pix[rp+k] < 0.0 {
				im.Pix[rp+k] = 0.0
			} else if im.Pix[rp+k] > 1.0 {
				im.Pix[rp+k] = 1.0
			}
		}
	}
}

// Miscellaneous contextual data that is used internaly by the varioius
// conversion functions.
type cvtFromFPContext struct {
	fp  *FPObject
	src *FPImage

	dstPix    []uint8
	dstStride int

	dstRGBA    *image.RGBA
	dstRGBA64  *image.RGBA64
	dstNRGBA64 *image.NRGBA64

	outputLUT_Xto8_Size  int
	outputLUT_Xto8       []uint8
	outputLUT_Xto32_Size int
	outputLUT_Xto32      []float32
}

type cvtOutputRowFunc func(cctx *cvtFromFPContext, j int)

func (fp *FPObject) convertOutputImageIndirect(cvtFn cvtOutputRowFunc, cctx *cvtFromFPContext) {
	for j := 0; j < (cctx.src.Rect.Max.Y - cctx.src.Rect.Min.Y); j++ {
		cvtFn(cctx, j)
	}
}

func convertFPToFinalFP_row(cctx *cvtFromFPContext, j int) {

	cctx.fp.postProcessImage_row(cctx.src, j)
	if cctx.fp.outputCCF == nil {
		return
	}

	for i := 0; i < (cctx.src.Rect.Max.X - cctx.src.Rect.Min.X); i++ {
		// Identify the slice of samples representing the pixel we're updating.
		sam := cctx.src.Pix[j*cctx.src.Stride+i*4 : j*cctx.src.Stride+i*4+4]

		if sam[3] <= 0.0 {
			// A fully transparent pixel (nothing to do)
			continue
		}

		// Convert to target colorspace
		cctx.fp.outputCCF(sam[0:3])
	}
}

// Convert in-place from:
//  * linear colorspace
//  * unassociated alpha
// to:
//  * target colorspace
//  * unassociated alpha
func (fp *FPObject) convertFPToFinalFP(im *FPImage) {
	cctx := new(cvtFromFPContext)
	cctx.fp = fp
	cctx.src = im

	if fp.outputCCF == nil {
		fp.progressMsgf("Post-processing image")
	} else {
		fp.progressMsgf("Converting to target colorspace")
	}

	fp.convertOutputImageIndirect(convertFPToFinalFP_row, cctx)
}

func convertFPToNRGBA_row(cctx *cvtFromFPContext, j int) {
	var k int

	cctx.fp.postProcessImage_row(cctx.src, j)

	for i := 0; i < (cctx.src.Rect.Max.X - cctx.src.Rect.Min.X); i++ {
		srcSam := cctx.src.Pix[j*cctx.src.Stride+i*4 : j*cctx.src.Stride+i*4+4]
		dstSam := cctx.dstPix[j*cctx.dstStride+i*4 : j*cctx.dstStride+i*4+4]

		// Set the alpha sample
		if !cctx.fp.hasTransparency {
			dstSam[3] = 255
		} else {
			dstSam[3] = uint8(srcSam[3]*255.0 + 0.5)
		}

		// Do colorspace conversion if needed.
		if cctx.fp.outputCCF != nil && dstSam[3] > 0 {
			if cctx.outputLUT_Xto8 != nil {
				// Do colorspace conversion using a lookup table.
				for k = 0; k < 3; k++ {
					dstSam[k] = cctx.outputLUT_Xto8[int(srcSam[k]*float32(cctx.outputLUT_Xto8_Size-1)+0.5)]
				}
				continue
			} else {
				// Do colorspace conversion the slow way.
				cctx.fp.outputCCF(srcSam[0:3])
			}
		}

		// Set the non-alpha samples (if we didn't use a lookup table).
		for k = 0; k < 3; k++ {
			dstSam[k] = uint8(srcSam[k]*255.0 + 0.5)
		}
	}
}

// src is floating point, linear colorspace, unassociated alpha
// dst is uint8, target colorspace, unassociated alpha
// It's okay to modify src's pixels; it's about to be thrown away.
func (fp *FPObject) convertFPToNRGBA_internal(src *FPImage, dstPix []uint8, dstStride int) {
	cctx := new(cvtFromFPContext)
	cctx.fp = fp
	cctx.src = src
	cctx.dstPix = dstPix
	cctx.dstStride = dstStride

	// This table size is optimized for sRGB. The sRGB curve's slope for
	// the darkest colors (the ones we're most concerned about) is 12.92,
	// so our table needs to have around 256*12.92 or more entries to ensure
	// that it includes every possible color value. A size of 255*12.92*3+1 =
	// 9885 improves precision, and makes the dark colors almost always
	// round correctly.
	cctx.outputLUT_Xto8_Size = 9885
	cctx.outputLUT_Xto8 = fp.makeOutputLUT_Xto8(cctx.outputLUT_Xto8_Size)

	if fp.outputCCF == nil {
		fp.progressMsgf("Converting to NRGBA format")
	} else {
		fp.progressMsgf("Converting to target colorspace, and NRGBA format")
	}

	fp.convertOutputImageIndirect(convertFPToNRGBA_row, cctx)
}

func (fp *FPObject) convertFPToNRGBA(src *FPImage) (dst *image.NRGBA) {
	dst = image.NewNRGBA(src.Bounds())
	fp.convertFPToNRGBA_internal(src, dst.Pix, dst.Stride)
	return
}

func convertFPToRGBA_row(cctx *cvtFromFPContext, j int) {
	var k int

	cctx.fp.postProcessImage_row(cctx.src, j)

	for i := 0; i < (cctx.src.Rect.Max.X - cctx.src.Rect.Min.X); i++ {
		srcSam := cctx.src.Pix[j*cctx.src.Stride+i*4 : j*cctx.src.Stride+i*4+4]
		dstSam := cctx.dstRGBA.Pix[j*cctx.dstRGBA.Stride+i*4 : j*cctx.dstRGBA.Stride+i*4+4]

		// Set the alpha sample
		if !cctx.fp.hasTransparency {
			dstSam[3] = 255
		} else {
			dstSam[3] = uint8(srcSam[3]*255.0 + 0.5)
		}

		// Do colorspace conversion if needed.
		if cctx.fp.outputCCF != nil && dstSam[3] > 0 {
			if cctx.outputLUT_Xto32 != nil {
				// Do colorspace conversion using a lookup table.
				for k = 0; k < 3; k++ {
					srcSam[k] = cctx.outputLUT_Xto32[int(srcSam[k]*float32(cctx.outputLUT_Xto32_Size-1)+0.5)]
				}
			} else {
				// Do colorspace conversion the slow way.
				cctx.fp.outputCCF(srcSam[0:3])
			}
		}

		// Set the non-alpha samples (converting to associated alpha)
		for k = 0; k < 3; k++ {
			dstSam[k] = uint8((srcSam[k]*srcSam[3])*255.0 + 0.5)
		}
	}
}

func (fp *FPObject) convertFPToRGBA_internal(src *FPImage) *image.RGBA {
	cctx := new(cvtFromFPContext)
	cctx.fp = fp
	cctx.src = src
	cctx.dstRGBA = image.NewRGBA(src.Bounds())

	// Because we still need to convert to associated alpha after doing color conversion,
	// the lookup table should return high-precision numbers -- uint8 is not enough.
	cctx.outputLUT_Xto32_Size = 9885
	cctx.outputLUT_Xto32 = fp.makeOutputLUT_Xto32(cctx.outputLUT_Xto32_Size)

	if fp.outputCCF == nil {
		fp.progressMsgf("Converting to RGBA format")
	} else {
		fp.progressMsgf("Converting to target colorspace, and RGBA format")
	}

	fp.convertOutputImageIndirect(convertFPToRGBA_row, cctx)
	return cctx.dstRGBA
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

func convertFPToNRGBA64_row(cctx *cvtFromFPContext, j int) {
	var dstSam [4]uint16
	var k int

	cctx.fp.postProcessImage_row(cctx.src, j)

	for i := 0; i < (cctx.src.Rect.Max.X - cctx.src.Rect.Min.X); i++ {
		srcSam := cctx.src.Pix[j*cctx.src.Stride+i*4 : j*cctx.src.Stride+i*4+4]

		// Set the alpha sample
		if !cctx.fp.hasTransparency {
			dstSam[3] = 65535
		} else {
			dstSam[3] = uint16(srcSam[3]*65535.0 + 0.5)
		}

		// Do colorspace conversion if needed.
		if cctx.fp.outputCCF != nil && dstSam[3] > 0 {
			cctx.fp.outputCCF(srcSam[0:3])
		}

		// Calculate the non-alpha samples.
		for k = 0; k < 3; k++ {
			dstSam[k] = uint16(srcSam[k]*65535.0 + 0.5)
		}

		// Convert all samples to NRGBA format
		dstPixelData := cctx.dstNRGBA64.Pix[j*cctx.dstNRGBA64.Stride+i*8 : j*cctx.dstNRGBA64.Stride+i*8+8]
		for k = 0; k < 4; k++ {
			dstPixelData[k*2] = uint8(dstSam[k] >> 8)
			dstPixelData[k*2+1] = uint8(dstSam[k] & 0xff)
		}
	}
}

func (fp *FPObject) convertFPToNRGBA64(src *FPImage) *image.NRGBA64 {
	cctx := new(cvtFromFPContext)
	cctx.fp = fp
	cctx.src = src
	cctx.dstNRGBA64 = image.NewNRGBA64(src.Bounds())

	if fp.outputCCF == nil {
		fp.progressMsgf("Converting to NRGBA64 format")
	} else {
		fp.progressMsgf("Converting to target colorspace, and NRGBA64 format")
	}

	fp.convertOutputImageIndirect(convertFPToNRGBA64_row, cctx)
	return cctx.dstNRGBA64
}

func convertFPToRGBA64_row(cctx *cvtFromFPContext, j int) {
	var dstSam [4]uint16
	var k int

	cctx.fp.postProcessImage_row(cctx.src, j)

	for i := 0; i < (cctx.src.Rect.Max.X - cctx.src.Rect.Min.X); i++ {
		srcSam := cctx.src.Pix[j*cctx.src.Stride+i*4 : j*cctx.src.Stride+i*4+4]

		// Set the alpha sample
		if !cctx.fp.hasTransparency {
			dstSam[3] = 65535
		} else {
			dstSam[3] = uint16(srcSam[3]*65535.0 + 0.5)
		}

		// Do colorspace conversion if needed.
		if cctx.fp.outputCCF != nil && dstSam[3] > 0 {
			cctx.fp.outputCCF(srcSam[0:3])
		}

		// Calculate the non-alpha samples.
		for k = 0; k < 3; k++ {
			dstSam[k] = uint16((srcSam[k]*srcSam[3])*65535.0 + 0.5)
		}

		// Convert all samples to NRGBA format
		dstPixelData := cctx.dstRGBA64.Pix[j*cctx.dstRGBA64.Stride+i*8 : j*cctx.dstRGBA64.Stride+i*8+8]
		for k = 0; k < 4; k++ {
			dstPixelData[k*2] = uint8(dstSam[k] >> 8)
			dstPixelData[k*2+1] = uint8(dstSam[k] & 0xff)
		}
	}
}

// TODO: If we are not going to use lookup tables to speed up color correction,
// this function and convertFPToNRGBA64 could be merged.
func (fp *FPObject) convertFPToRGBA64(src *FPImage) *image.RGBA64 {
	cctx := new(cvtFromFPContext)
	cctx.fp = fp
	cctx.src = src
	cctx.dstRGBA64 = image.NewRGBA64(src.Bounds())

	if fp.outputCCF == nil {
		fp.progressMsgf("Converting to RGBA64 format")
	} else {
		fp.progressMsgf("Converting to target colorspace, and RGBA64 format")
	}

	fp.convertOutputImageIndirect(convertFPToRGBA64_row, cctx)
	return cctx.dstRGBA64
}
