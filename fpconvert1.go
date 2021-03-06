// ◄◄◄ fpconvert1.go ►►►
// Copyright © 2012 Jason Summers

// Functions for converting from a standard image.Image object, to our
// internal format, prior to doing the resize.

// Most of this code is related to improving speed. It could be much smaller and
// simpler if we didn't care how slow it ran.

package fpresize

import "image"
import "image/color"
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

// Data that is constant for all workers.
type convertSrcWorkContext struct {
	inputLUT_8to32  []float32
	inputLUT_16to32 []float32
	dst             *FPImage
	srcImage        image.Image
	src_AsRGBA      *image.RGBA
	src_AsNRGBA     *image.NRGBA
	src_AsYCbCr     *image.YCbCr
	src_AsGray      *image.Gray
	cvtRowFn        func(fp *FPObject, cctx *convertSrcWorkContext, j int)
}

type convertSrcWorkItem struct {
	j       int
	stopNow bool
}

// Convert row j from fp.srcImage to wc.dst.
func convertSrcRow_Any(fp *FPObject, wc *convertSrcWorkContext, j int) {
	var srcSam16 [4]uint32 // Source RGBA samples (uint16 stored in uint32)
	var k int

	for i := 0; i < fp.srcW; i++ {
		// Read a pixel from the source image, into uint16 samples
		srcclr := wc.srcImage.At(fp.srcBounds.Min.X+i, fp.srcBounds.Min.Y+j)
		srcSam16[0], srcSam16[1], srcSam16[2], srcSam16[3] = srcclr.RGBA()

		if srcSam16[3] < 65535 {
			fp.srcHasTransparency = true
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

// Convert row j from wc.src_AsGray to wc.dst.
// This is an optimized version of convertSrcRow_Any().
func convertSrcRow_Gray(fp *FPObject, wc *convertSrcWorkContext, j int) {
	for i := 0; i < fp.srcW; i++ {
		srcPix := wc.src_AsGray.Pix[wc.src_AsGray.Stride*j+i]

		// Identify the slice of samples representing this pixel in the
		// converted image.
		dstSam := wc.dst.Pix[j*wc.dst.Stride+i*4 : j*wc.dst.Stride+i*4+4]

		// Do color correction, if necessary.
		if fp.inputCCF != nil && wc.inputLUT_8to32 != nil {
			// Convert to linear color, using a lookup table.
			dstSam[0] = wc.inputLUT_8to32[srcPix]
		} else {
			// In all other cases, first copy the uncorrected sample to dstSam.
			dstSam[0] = float32(srcPix) / 255.0

			if fp.inputCCF != nil {
				// Convert to linear color, without a lookup table.
				fp.inputCCF(dstSam[0:1])
			}
		}
		dstSam[1] = dstSam[0]
		dstSam[2] = dstSam[0]
		dstSam[3] = 1.0
	}
}

// Convert row j from wc.src_AsNRGBA to wc.dst.
// This is an optimized version of convertSrcRow_Any().
func convertSrcRow_NRGBA(fp *FPObject, wc *convertSrcWorkContext, j int) {
	var k int

	for i := 0; i < fp.srcW; i++ {
		var srcSam8 []uint8
		srcSam8 = wc.src_AsNRGBA.Pix[wc.src_AsNRGBA.Stride*j+4*i : wc.src_AsNRGBA.Stride*j+4*i+4]

		if srcSam8[3] < 255 {
			fp.srcHasTransparency = true

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
// This is an optimized version of convertSrcRow_Any().
func convertSrcRow_RGBA(fp *FPObject, wc *convertSrcWorkContext, j int) {
	var k int

	for i := 0; i < fp.srcW; i++ {
		var srcSam8 []uint8
		srcSam8 = wc.src_AsRGBA.Pix[wc.src_AsRGBA.Stride*j+4*i : wc.src_AsRGBA.Stride*j+4*i+4]

		if srcSam8[3] < 255 {
			fp.srcHasTransparency = true

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

// Convert row j from wc.src_AsYCbCrto wc.dst.
// This is an optimized version of convertSrcRow_Any(), useful for images that
// were read from JPEG files.
//
// Although not very optimized, it's still well over twice as fast as using
// convertSrcRow_Any would be.
func convertSrcRow_YCbCr(fp *FPObject, wc *convertSrcWorkContext, j int) {
	var k int

	for i := 0; i < fp.srcW; i++ {
		var srcY, srcCb, srcCr uint8
		var srcRGB [3]uint8

		yOffs := wc.src_AsYCbCr.YOffset(fp.srcBounds.Min.X+i, fp.srcBounds.Min.Y+j)
		cOffs := wc.src_AsYCbCr.COffset(fp.srcBounds.Min.X+i, fp.srcBounds.Min.Y+j)

		srcY = wc.src_AsYCbCr.Y[yOffs]
		srcCb = wc.src_AsYCbCr.Cb[cOffs]
		srcCr = wc.src_AsYCbCr.Cr[cOffs]

		srcRGB[0], srcRGB[1], srcRGB[2] = color.YCbCrToRGB(srcY, srcCb, srcCr)

		// Identify the slice of samples representing this pixel in the
		// converted image.
		dstSam := wc.dst.Pix[j*wc.dst.Stride+4*i : j*wc.dst.Stride+4*i+4]

		if fp.inputCCF != nil && wc.inputLUT_8to32 != nil {
			// Convert to linear color, using a lookup table.
			for k = 0; k < 3; k++ {
				dstSam[k] = wc.inputLUT_8to32[srcRGB[k]]
			}
		} else {
			// In all other cases, first copy the uncorrected samples into dstSam
			for k = 0; k < 3; k++ {
				dstSam[k] = float32(srcRGB[k]) / 255.0
			}

			if fp.inputCCF != nil {
				// Convert to linear color, without a lookup table.
				fp.inputCCF(dstSam[0:3])
			}
		}
		dstSam[3] = 1.0 // YCbCr is always opaque
	}
}

func (fp *FPObject) convertSrcWorker(wc *convertSrcWorkContext, workQueue chan convertSrcWorkItem) {
	for {
		wi := <-workQueue
		if wi.stopNow {
			return
		}

		wc.cvtRowFn(fp, wc, wi.j)
	}
}

// Copies(&converts) from fp.srcImg to the given image.
func (fp *FPObject) convertSrc(src image.Image, dst *FPImage) error {
	var i int
	var j int
	var nSamples int
	var wi convertSrcWorkItem

	if int64(fp.srcW)*int64(fp.srcH) > maxImagePixels {
		return errors.New("Source image too large to process")
	}

	wc := new(convertSrcWorkContext)
	wc.dst = dst
	wc.srcImage = src

	// Look at the underlying type of fp.srcImage, and prepare a conversion
	// strategy.
	fp.srcHasColor = true
	switch wc.srcImage.(type) {
	case *image.NRGBA:
		wc.src_AsNRGBA = wc.srcImage.(*image.NRGBA)
		wc.cvtRowFn = convertSrcRow_NRGBA
		wc.inputLUT_8to32 = fp.makeInputLUT_Xto32(256)
	case *image.RGBA:
		wc.src_AsRGBA = wc.srcImage.(*image.RGBA)
		wc.cvtRowFn = convertSrcRow_RGBA
		wc.inputLUT_8to32 = fp.makeInputLUT_Xto32(256)
	case *image.YCbCr:
		wc.src_AsYCbCr = wc.srcImage.(*image.YCbCr)
		wc.cvtRowFn = convertSrcRow_YCbCr
		wc.inputLUT_8to32 = fp.makeInputLUT_Xto32(256)
	case *image.Gray:
		wc.src_AsGray = wc.srcImage.(*image.Gray)
		wc.cvtRowFn = convertSrcRow_Gray
		wc.inputLUT_8to32 = fp.makeInputLUT_Xto32(256)
		fp.srcHasColor = false
	case *image.Gray16:
		wc.cvtRowFn = convertSrcRow_Any
		wc.inputLUT_16to32 = fp.makeInputLUT_Xto32(65536)
		fp.srcHasColor = false
	default:
		wc.cvtRowFn = convertSrcRow_Any
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

	workQueue := make(chan convertSrcWorkItem)

	for i = 0; i < fp.numWorkers; i++ {
		go fp.convertSrcWorker(wc, workQueue)
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
