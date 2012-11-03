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
	if fp.dstCanvasW*fp.dstCanvasH < (tableSize/4)*fp.numWorkers {
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
		// This is not perfect.
		// Of the 256 available target-colorspace values, what we really want
		// is the one that is nearest in a *linear* colorspace.
		// But here we are converting to the target colorspace, then choosing
		// the nearest color in that colorspace.
		// The likely effect is that a small percentage of samples are one
		// shade too light. With 256 shades, that's too small a difference to
		// be noticeable. And doing it correctly could be quite a bit slower.
		// (This issue occurs for all non-floating-point target image types,
		// not just those that use this code specifically.)
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
	if fp.dstCanvasW*fp.dstCanvasH < (tableSize/4)*fp.numWorkers {
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
//  * associated alpha, linear colorspace, some samples may not be valid
// Convert to 
//  * unassociated alpha, linear colorspace, samples always valid,
//    all samples clamped to [0,1].
//
// It is possible that we will convert to unassociated alpha needlessly, only
// to convert right back to associated alpha. That will happen if color
// correction is disabled, and the final image format uses associated alpha.
// We may optimize for that case in a future version.
func (fp *FPObject) postProcessRow(im *FPImage, j int) {
	var k int

	for i := 0; i < (im.Rect.Max.X - im.Rect.Min.X); i++ {
		rp := j*im.Stride + i*4 // index of the Red sample in im.Pix
		ap := rp + 3            // index of the alpha sample

		if !fp.mustProcessColor {
			// If the green and blue samples aren't valid (for grayscale
			// images, we may only process the red channel), make them valid.
			//
			// For performance reasons, it would be better to do this *after*
			// colorspace conversion, instead of here. But that would make a
			// mess of the code, and in most cases the speed improvement would
			// be very small.
			im.Pix[rp+1] = im.Pix[rp]
			im.Pix[rp+2] = im.Pix[rp]
		}

		if !fp.mustProcessTransparency {
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
type convertDstWorkContext struct {
	src *FPImage

	cvtRowFn func(fp *FPObject, wc *convertDstWorkContext, j int)

	dstPix    []uint8
	dstStride int

	dstRGBA    *image.RGBA
	dstRGBA64  *image.RGBA64
	dstNRGBA64 *image.NRGBA64
	dstGray    *image.Gray
	dstGray16  *image.Gray16
	isNRGBA64  bool

	outputLUT_Xto8_Size  int
	outputLUT_Xto8       []uint8
	outputLUT_Xto32_Size int
	outputLUT_Xto32      []float32
}

type convertDstWorkItem struct {
	j       int
	stopNow bool
}

func (fp *FPObject) convertDstWorker(wc *convertDstWorkContext, workQueue chan convertDstWorkItem) {
	for {
		wi := <-workQueue
		if wi.stopNow {
			return
		}

		wc.cvtRowFn(fp, wc, wi.j)
	}
}

func (fp *FPObject) convertDstIndirect(wc *convertDstWorkContext) {
	var i, j int
	var wi convertDstWorkItem

	workQueue := make(chan convertDstWorkItem)

	for i = 0; i < fp.numWorkers; i++ {
		go fp.convertDstWorker(wc, workQueue)
	}

	// Each row is a "work item". Send each row to a worker.
	for j = 0; j < (wc.src.Rect.Max.Y - wc.src.Rect.Min.Y); j++ {
		wi.j = j
		workQueue <- wi
	}

	// Send out a "stop work" order.
	wi.stopNow = true
	for i = 0; i < fp.numWorkers; i++ {
		workQueue <- wi
	}
}

func convertDstRow_FP(fp *FPObject, wc *convertDstWorkContext, j int) {
	fp.postProcessRow(wc.src, j)
	if fp.outputCCF == nil {
		return
	}

	for i := 0; i < (wc.src.Rect.Max.X - wc.src.Rect.Min.X); i++ {
		// Identify the slice of samples representing the pixel we're updating.
		sam := wc.src.Pix[j*wc.src.Stride+i*4 : j*wc.src.Stride+i*4+4]

		if sam[3] <= 0.0 {
			// A fully transparent pixel (nothing to do)
			continue
		}

		// Convert to target colorspace
		fp.outputCCF(sam[0:3])
	}
}

// The target image will still be an FPImage, but we need to do some post-
// processing: convert to unassociated alpha, and probably convert from linear
// color to the target colorspace. The conversion is in-place.
func (fp *FPObject) convertDst_FP(im *FPImage) {
	wc := new(convertDstWorkContext)
	wc.src = im

	if fp.outputCCF == nil {
		fp.progressMsgf("Post-processing image")
	} else {
		fp.progressMsgf("Converting to target colorspace")
	}

	wc.cvtRowFn = convertDstRow_FP
	fp.convertDstIndirect(wc)
}

func convertDstRow_NRGBA(fp *FPObject, wc *convertDstWorkContext, j int) {
	var k int

	fp.postProcessRow(wc.src, j)

	for i := 0; i < (wc.src.Rect.Max.X - wc.src.Rect.Min.X); i++ {
		srcSam := wc.src.Pix[j*wc.src.Stride+i*4 : j*wc.src.Stride+i*4+4]
		dstSam := wc.dstPix[j*wc.dstStride+i*4 : j*wc.dstStride+i*4+4]

		// Set the alpha sample
		if !fp.mustProcessTransparency {
			dstSam[3] = 255
		} else {
			dstSam[3] = uint8(srcSam[3]*255.0 + 0.5)
		}

		// Do colorspace conversion if needed.
		if fp.outputCCF != nil && dstSam[3] > 0 {
			if wc.outputLUT_Xto8 != nil {
				// Do colorspace conversion using a lookup table.
				for k = 0; k < 3; k++ {
					dstSam[k] = wc.outputLUT_Xto8[int(srcSam[k]*float32(wc.outputLUT_Xto8_Size-1)+0.5)]
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

// src is floating point, linear colorspace, unassociated alpha
// dst is uint8, target colorspace, unassociated alpha
// It's okay to modify src's pixels; it's about to be thrown away.
func (fp *FPObject) convertDst_NRGBA_internal(src *FPImage, dstPix []uint8, dstStride int,
	formatName string) {
	wc := new(convertDstWorkContext)
	wc.src = src
	wc.dstPix = dstPix
	wc.dstStride = dstStride

	// This table size is optimized for sRGB. The sRGB curve's slope for
	// the darkest colors (the ones we're most concerned about) is 12.92,
	// so our table needs to have around 256*12.92 or more entries to ensure
	// that it includes every possible color value. A size of 9885 ≈
	// 255*12.92*3+1 improves precision, and makes the dark colors almost always
	// round correctly.
	wc.outputLUT_Xto8_Size = 9885
	wc.outputLUT_Xto8 = fp.makeOutputLUT_Xto8(wc.outputLUT_Xto8_Size)

	if fp.outputCCF == nil {
		fp.progressMsgf("Converting to %s format", formatName)
	} else {
		fp.progressMsgf("Converting to target colorspace, and %s format", formatName)
	}

	wc.cvtRowFn = convertDstRow_NRGBA
	fp.convertDstIndirect(wc)
}

func (fp *FPObject) convertDst_NRGBA(src *FPImage) (dst *image.NRGBA) {
	dst = image.NewNRGBA(src.Bounds())
	fp.convertDst_NRGBA_internal(src, dst.Pix, dst.Stride, "NRGBA")
	return
}

func convertDstRow_RGBA(fp *FPObject, wc *convertDstWorkContext, j int) {
	var k int

	fp.postProcessRow(wc.src, j)

	for i := 0; i < (wc.src.Rect.Max.X - wc.src.Rect.Min.X); i++ {
		srcSam := wc.src.Pix[j*wc.src.Stride+i*4 : j*wc.src.Stride+i*4+4]
		dstSam := wc.dstRGBA.Pix[j*wc.dstRGBA.Stride+i*4 : j*wc.dstRGBA.Stride+i*4+4]

		// Set the alpha sample
		if !fp.mustProcessTransparency {
			dstSam[3] = 255
		} else {
			dstSam[3] = uint8(srcSam[3]*255.0 + 0.5)
		}

		// Do colorspace conversion if needed.
		if fp.outputCCF != nil && dstSam[3] > 0 {
			if wc.outputLUT_Xto32 != nil {
				// Do colorspace conversion using a lookup table.
				for k = 0; k < 3; k++ {
					srcSam[k] = wc.outputLUT_Xto32[int(srcSam[k]*float32(wc.outputLUT_Xto32_Size-1)+0.5)]
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

func (fp *FPObject) convertDst_RGBA(src *FPImage) *image.RGBA {
	if !fp.mustProcessTransparency {
		// If the image has no transparency, use convertDst_NRGBA_internal,
		// which is usually somewhat faster.
		dst := image.NewRGBA(src.Bounds())
		fp.convertDst_NRGBA_internal(src, dst.Pix, dst.Stride, "RGB")
		return dst
	}

	wc := new(convertDstWorkContext)
	wc.src = src
	wc.dstRGBA = image.NewRGBA(src.Bounds())

	// Because we still need to convert to associated alpha after doing color conversion,
	// the lookup table should return high-precision numbers -- uint8 is not enough.
	wc.outputLUT_Xto32_Size = 9885
	wc.outputLUT_Xto32 = fp.makeOutputLUT_Xto32(wc.outputLUT_Xto32_Size)

	if fp.outputCCF == nil {
		fp.progressMsgf("Converting to RGBA format")
	} else {
		fp.progressMsgf("Converting to target colorspace, and RGBA format")
	}

	wc.cvtRowFn = convertDstRow_RGBA
	fp.convertDstIndirect(wc)
	return wc.dstRGBA
}

func convertDstRow_RGBA64orNRGBA64(fp *FPObject, wc *convertDstWorkContext, j int) {
	var dstSam [4]uint16
	var dstPixelData []uint8
	var k int

	fp.postProcessRow(wc.src, j)

	for i := 0; i < (wc.src.Rect.Max.X - wc.src.Rect.Min.X); i++ {
		srcSam := wc.src.Pix[j*wc.src.Stride+i*4 : j*wc.src.Stride+i*4+4]

		// Set the alpha sample
		if !fp.mustProcessTransparency {
			dstSam[3] = 65535
		} else {
			dstSam[3] = uint16(srcSam[3]*65535.0 + 0.5)
		}

		// Do colorspace conversion if needed.
		if fp.outputCCF != nil && dstSam[3] > 0 {
			fp.outputCCF(srcSam[0:3])
		}

		if wc.isNRGBA64 {
			// Calculate the non-alpha samples.
			for k = 0; k < 3; k++ {
				dstSam[k] = uint16(srcSam[k]*65535.0 + 0.5)
			}
			// Locate this pixel in the target image.
			dstPixelData = wc.dstNRGBA64.Pix[j*wc.dstNRGBA64.Stride+i*8 : j*wc.dstNRGBA64.Stride+i*8+8]
		} else { // RGBA64 format
			for k = 0; k < 3; k++ {
				dstSam[k] = uint16((srcSam[k]*srcSam[3])*65535.0 + 0.5)
			}
			dstPixelData = wc.dstRGBA64.Pix[j*wc.dstRGBA64.Stride+i*8 : j*wc.dstRGBA64.Stride+i*8+8]
		}

		// Convert all samples to final RGBA/NRGBA format (big endian uint16)
		for k = 0; k < 4; k++ {
			dstPixelData[k*2] = uint8(dstSam[k] >> 8)
			dstPixelData[k*2+1] = uint8(dstSam[k] & 0xff)
		}
	}
}

func (fp *FPObject) convertDst_NRGBA64(src *FPImage) *image.NRGBA64 {
	wc := new(convertDstWorkContext)
	wc.src = src
	wc.isNRGBA64 = true
	wc.dstNRGBA64 = image.NewNRGBA64(src.Bounds())

	if fp.outputCCF == nil {
		fp.progressMsgf("Converting to NRGBA64 format")
	} else {
		fp.progressMsgf("Converting to target colorspace, and NRGBA64 format")
	}

	wc.cvtRowFn = convertDstRow_RGBA64orNRGBA64
	fp.convertDstIndirect(wc)
	return wc.dstNRGBA64
}

func (fp *FPObject) convertDst_RGBA64(src *FPImage) *image.RGBA64 {
	wc := new(convertDstWorkContext)
	wc.src = src
	wc.isNRGBA64 = false
	wc.dstRGBA64 = image.NewRGBA64(src.Bounds())

	if fp.outputCCF == nil {
		fp.progressMsgf("Converting to RGBA64 format")
	} else {
		fp.progressMsgf("Converting to target colorspace, and RGBA64 format")
	}

	wc.cvtRowFn = convertDstRow_RGBA64orNRGBA64
	fp.convertDstIndirect(wc)
	return wc.dstRGBA64
}

func convertDstRow_Gray(fp *FPObject, wc *convertDstWorkContext, j int) {
	var tmpPix [3]float32

	for i := 0; i < (wc.src.Rect.Max.X - wc.src.Rect.Min.X); i++ {
		srcVal := wc.src.Pix[j*wc.src.Stride+i*4]
		// Since we didn't call postProcessRow(), do the little bit of
		// post-processing needed for grayscale images.
		if srcVal < 0.0 {
			srcVal = 0.0
		} else if srcVal > 1.0 {
			srcVal = 1.0
		}

		dstSamPos := j*wc.dstGray.Stride + i // Index into wc.dstGray.Pix

		// Do colorspace conversion if needed.
		if fp.outputCCF != nil {
			if wc.outputLUT_Xto8 != nil {
				// Do colorspace conversion using a lookup table.
				wc.dstGray.Pix[dstSamPos] = wc.outputLUT_Xto8[int(srcVal*float32(wc.outputLUT_Xto8_Size-1)+0.5)]
				continue
			} else {
				// Do colorspace conversion the slow way.
				tmpPix[0] = srcVal
				if (fp.outputCCFFlags & CCFFlagWholePixels) != 0 {
					tmpPix[1] = srcVal
					tmpPix[2] = srcVal
					fp.outputCCF(tmpPix[0:3])
				} else {
					fp.outputCCF(tmpPix[0:1])
				}
				srcVal = tmpPix[0]
			}
		}
		wc.dstGray.Pix[dstSamPos] = uint8(srcVal*255.0 + 0.5)
	}
}

func (fp *FPObject) convertDst_Gray(src *FPImage) *image.Gray {
	wc := new(convertDstWorkContext)
	wc.src = src
	wc.dstGray = image.NewGray(src.Bounds())

	wc.outputLUT_Xto8_Size = 9885
	wc.outputLUT_Xto8 = fp.makeOutputLUT_Xto8(wc.outputLUT_Xto8_Size)

	if fp.outputCCF == nil {
		fp.progressMsgf("Converting to Gray format")
	} else {
		fp.progressMsgf("Converting to target colorspace, and Gray format")
	}

	wc.cvtRowFn = convertDstRow_Gray
	fp.convertDstIndirect(wc)
	return wc.dstGray
}

func convertDstRow_Gray16(fp *FPObject, wc *convertDstWorkContext, j int) {
	var tmpPix [3]float32

	fp.postProcessRow(wc.src, j)

	for i := 0; i < (wc.src.Rect.Max.X - wc.src.Rect.Min.X); i++ {
		srcVal := wc.src.Pix[j*wc.src.Stride+i*4]

		// Do colorspace conversion if needed.
		if fp.outputCCF != nil {
			tmpPix[0] = srcVal
			if (fp.outputCCFFlags & CCFFlagWholePixels) != 0 {
				tmpPix[1] = srcVal
				tmpPix[2] = srcVal
				fp.outputCCF(tmpPix[0:3])
			} else {
				fp.outputCCF(tmpPix[0:1])
			}
			srcVal = tmpPix[0]
		}

		dstVal16 := uint16(srcVal*65535.0 + 0.5)
		wc.dstGray16.Pix[j*wc.dstGray16.Stride+i*2] = uint8(dstVal16 >> 8)
		wc.dstGray16.Pix[j*wc.dstGray16.Stride+i*2+1] = uint8(dstVal16 & 0xff)
	}
}

func (fp *FPObject) convertDst_Gray16(src *FPImage) *image.Gray16 {
	wc := new(convertDstWorkContext)
	wc.src = src
	wc.dstGray16 = image.NewGray16(src.Bounds())

	if fp.outputCCF == nil {
		fp.progressMsgf("Converting to Gray16 format")
	} else {
		fp.progressMsgf("Converting to target colorspace, and Gray16 format")
	}

	wc.cvtRowFn = convertDstRow_Gray16
	fp.convertDstIndirect(wc)
	return wc.dstGray16
}
