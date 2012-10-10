// ◄◄◄ fpconvert2.go ►►►
// Copyright © 2012 Jason Summers

// Functions converting from our FPImage format.

// Most of this code is related to improving speed. It could be much smaller and
// simpler if we didn't care how slow it ran.

package fpresize

import "image"

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

type cvtOutputRowFunc func(cctx *cvtFromFPContext, j int)

// Miscellaneous contextual data that is used internaly by the varioius
// conversion functions.
type cvtFromFPContext struct {
	fp  *FPObject
	src *FPImage

	cvtRowFn cvtOutputRowFunc

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

type toOutputWorkItem struct {
	j       int
	stopNow bool
}

func (fp *FPObject) toOutputWorker(cctx *cvtFromFPContext, workQueue chan toOutputWorkItem) {
	for {
		wi := <-workQueue
		if wi.stopNow {
			return
		}

		cctx.cvtRowFn(cctx, wi.j)
	}
}

func (fp *FPObject) convertOutputImageIndirect(cctx *cvtFromFPContext) {
	var i, j int
	var wi toOutputWorkItem

	workQueue := make(chan toOutputWorkItem)

	for i = 0; i < fp.numWorkers; i++ {
		go fp.toOutputWorker(cctx, workQueue)
	}

	// Each row is a "work item". Send each row to a worker.
	for j = 0; j < (cctx.src.Rect.Max.Y - cctx.src.Rect.Min.Y); j++ {
		wi.j = j
		workQueue <- wi
	}

	// Send out a "stop work" order.
	wi.stopNow = true
	for i = 0; i < fp.numWorkers; i++ {
		workQueue <- wi
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

	cctx.cvtRowFn = convertFPToFinalFP_row
	fp.convertOutputImageIndirect(cctx)
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

	cctx.cvtRowFn = convertFPToNRGBA_row
	fp.convertOutputImageIndirect(cctx)
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

	cctx.cvtRowFn = convertFPToRGBA_row
	fp.convertOutputImageIndirect(cctx)
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

	cctx.cvtRowFn = convertFPToNRGBA64_row
	fp.convertOutputImageIndirect(cctx)
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
// this method and convertFPToNRGBA64 could be merged.
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

	cctx.cvtRowFn = convertFPToRGBA64_row
	fp.convertOutputImageIndirect(cctx)
	return cctx.dstRGBA64
}
