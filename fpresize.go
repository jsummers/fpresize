// ◄◄◄ fpresize.go ►►►
// Copyright © 2012 Jason Summers

// fpresize performs high-quality resizing of raster images.
package fpresize

import "image"
import "image/color"
import "math"
import "errors"

// FPObject is an opaque struct that tracks the state of the resize operation.
// There usually should be one FPObject per source image.
type FPObject struct {
	srcImage   image.Image
	srcFPImage *FPImage
	dstFPImage *FPImage
	srcBounds  image.Rectangle
	dstBounds  image.Rectangle
	srcW, srcH int
	dstW, dstH int

	hasTransparency bool

	filterGetter FilterGetter
	blurGetter   BlurGetter
	inputCCFSet  bool
	inputCCF     ColorConverter
	outputCCFSet bool
	outputCCF    ColorConverter
}

// A ColorConverter is passed a slice of three samples (R, G, B),
// and converts them to a different colorspace in-place.
type ColorConverter func(x []float64)

// A FilterGetter is a function that returns a Filter. The Filter
// returned can depend on which dimension is being resized, and the
// scale factor.
type FilterGetter func(isVertical bool, scaleFactor float64) *Filter

// A BlurGetter is a function that returns a 'blur' setting.
type BlurGetter func(isVertical bool, scaleFactor float64) float64

type fpWeight struct {
	srcSam int
	dstSam int
	weight float32
}

// For convenience, we (usually) don't supply negative arguments to filters.
func fixupFilterArg(filterFlags uint32, x float64) float64 {
	if filterFlags&FilterFlagAsymmetric != 0 {
		return x
	}
	if x < 0.0 {
		return -x
	}
	return x
}

// Create and return a weightlist using fp.filter.
// scrN, dstN = number of samples
func (fp *FPObject) createWeightList(srcN, dstN int, isVertical bool) []fpWeight {
	var reductionFactor float64
	var radius float64
	var weightList []fpWeight
	var weightsUsed int
	var weightListCap int
	var srcN_flt float64 = float64(srcN)
	var dstN_flt float64 = float64(dstN)
	var scaleFactor float64 = dstN_flt / srcN_flt
	var filter *Filter
	var filterFlags uint32

	if dstN < srcN {
		reductionFactor = srcN_flt / dstN_flt
	} else {
		reductionFactor = 1.0
	}
	if fp.blurGetter != nil {
		reductionFactor *= fp.blurGetter(isVertical, scaleFactor)
	}

	if fp.filterGetter != nil {
		filter = fp.filterGetter(isVertical, scaleFactor)
	}
	if filter == nil {
		// Our default filter
		filter = MakeLanczosFilter(2)
	}

	radius = filter.Radius(scaleFactor)

	if filter.Flags != nil {
		filterFlags = filter.Flags(scaleFactor)
	}

	// TODO: Review this formula to make sure it's good enough,
	// and/or dynamically increase the capacity of the weightList
	// slice on demand.
	weightListCap = int((1.01+2.0*radius*reductionFactor)*dstN_flt) + 2
	weightList = make([]fpWeight, weightListCap)

	var dstSamIdx int
	var posInSrc float64
	var firstSrcSamIdx int
	var lastSrcSamIdx int
	var idxOfFirstWeight int
	var v float64
	var v_norm float64
	var v_count int
	var srcSamIdx int
	var arg float64
	var i int

	for dstSamIdx = 0; dstSamIdx < dstN; dstSamIdx++ {
		// Figure out the range of source samples that are relevent to this dst sample.
		posInSrc = ((0.5+float64(dstSamIdx))/dstN_flt)*srcN_flt - 0.5
		firstSrcSamIdx = int(math.Ceil(posInSrc - radius*reductionFactor - 0.0001))
		lastSrcSamIdx = int(math.Floor(posInSrc + radius*reductionFactor + 0.0001))

		// Remember which item in the weightlist was the first one for this
		// target sample.
		idxOfFirstWeight = weightsUsed

		v_norm = 0.0
		v_count = 0

		// Iterate through the input samples that affect this output sample
		for srcSamIdx = firstSrcSamIdx; srcSamIdx <= lastSrcSamIdx; srcSamIdx++ {
			if srcSamIdx < 0 || srcSamIdx >= srcN {
				continue
			}

			arg = (float64(srcSamIdx) - posInSrc) / reductionFactor

			v = filter.F(fixupFilterArg(filterFlags, arg), scaleFactor)
			if v == 0.0 {
				continue
			}
			v_norm += v
			v_count++

			// Add this weight to the list (it will be normalized later)
			weightList[weightsUsed].srcSam = srcSamIdx
			weightList[weightsUsed].dstSam = dstSamIdx
			weightList[weightsUsed].weight = float32(v)
			weightsUsed++
		}

		if v_count == 0 {
			continue
		}

		// Normalize the weights we just added

		if math.Abs(v_norm) < 0.000001 {
			// This shouldn't happen (with a sane filter), but just to protect
			// against division-by-zero...
			v_norm = 0.000001
		}

		for i = idxOfFirstWeight; i < weightsUsed; i++ {
			weightList[i].weight /= float32(v_norm)
		}
	}

	weightList = weightList[0:weightsUsed] // Re-slice, to set len(weightList)
	return weightList
}

// A reference to a set of samples within (presumably) a FPImage object.
// pix[stride*0] is the first sample; pix[stride*1] is the next, ...
type sample1dRef struct {
	sam    []float32
	stride int
}

// Use the given weightlist to resize a set of samples onto another set of samples.
func resample1d(src1d *sample1dRef, dst1d *sample1dRef, weightList []fpWeight) {
	for i := range weightList {
		// This is the line of code that actually does the resampling. (But All the
		// interesting things were precalculated in createWeightList().)
		dst1d.sam[weightList[i].dstSam*dst1d.stride] += src1d.sam[weightList[i].srcSam*
			src1d.stride] * weightList[i].weight
	}
}

// Create dst, an image with a different height than src.
// dst is a zeroed-out struct, created by the caller.
// resizeHeight sets its fields, and makes its origin (0,0).
func (fp *FPObject) resizeHeight(src *FPImage, dst *FPImage, dstH int) {
	var nSamples int
	var src1d sample1dRef
	var dst1d sample1dRef
	var srcH int
	var w int // width of both images

	w = src.Rect.Max.X - src.Rect.Min.X
	srcH = src.Rect.Max.Y - src.Rect.Min.Y

	dst.Rect.Min.X = 0
	dst.Rect.Min.Y = 0
	dst.Rect.Max.X = w
	dst.Rect.Max.Y = dstH

	dst.Stride = w * 4
	nSamples = dst.Stride * dstH
	dst.Pix = make([]float32, nSamples)

	weightList := fp.createWeightList(srcH, dstH, true)

	// Iterate over the columns (of which src and dst have the same number)
	// Columns of *samples*, that is, not pixels.
	src1d.stride = src.Stride
	dst1d.stride = dst.Stride
	for col := 0; col < 4*w; col++ {
		if fp.hasTransparency || (col%4 != 3) { // If no transparency, skip over the alpha samples
			src1d.sam = src.Pix[col:]
			dst1d.sam = dst.Pix[col:]
			resample1d(&src1d, &dst1d, weightList)
		}
	}
}

// Create dst, an image with a different width than src.
// TODO: Maybe merge resizeWidth & resizeHeight
func (fp *FPObject) resizeWidth(src *FPImage, dst *FPImage, dstW int) {
	var nSamples int
	var src1d sample1dRef
	var dst1d sample1dRef
	var srcW int
	var h int // height of both images

	srcW = src.Rect.Max.X - src.Rect.Min.X
	h = src.Rect.Max.Y - src.Rect.Min.Y

	dst.Rect.Min.X = 0
	dst.Rect.Min.Y = 0
	dst.Rect.Max.X = dstW
	dst.Rect.Max.Y = h
	dst.Stride = dstW * 4
	nSamples = dst.Stride * h
	dst.Pix = make([]float32, nSamples)

	weightList := fp.createWeightList(srcW, dstW, false)

	// Iterate over the rows (of which src and dst have the same number)
	src1d.stride = 4
	dst1d.stride = 4
	for row := 0; row < h; row++ {
		// Iterate over R,G,B,A
		for k := 0; k < 4; k++ {
			if fp.hasTransparency || k != 3 {
				src1d.sam = src.Pix[row*src.Stride+k:]
				dst1d.sam = dst.Pix[row*dst.Stride+k:]
				resample1d(&src1d, &dst1d, weightList)
			}
		}
	}
}

// Copies(&converts) from fp.srcImg to the given image.
func (fp *FPObject) copySrcToFPImage(im *FPImage) error {
	var i, j int
	var nSamples int
	var r, g, b, a uint32
	// We prefer to use high-precision (float64) for intermediate values
	// when doing colorspace conversion.
	var clr [4]float64
	var srcclr color.Color

	if int64(fp.srcW)*int64(fp.srcH) > maxImagePixels {
		return errors.New("Source image too large to process")
	}

	// Allocate the pixel array
	im.Rect.Min.X = 0
	im.Rect.Min.Y = 0
	im.Rect.Max.X = fp.srcW
	im.Rect.Max.Y = fp.srcH
	im.Stride = fp.srcW * 4
	nSamples = im.Stride * fp.srcH
	im.Pix = make([]float32, nSamples)

	for j = 0; j < fp.srcH; j++ {
		for i = 0; i < fp.srcW; i++ {
			// TODO: Maybe we should be using fpModel.Convert here, in some way.
			srcclr = fp.srcImage.At(fp.srcBounds.Min.X+i, fp.srcBounds.Min.Y+j)
			r, g, b, a = srcclr.RGBA()
			if a < 65535 {
				fp.hasTransparency = true
			}

			// Choose from among several methods of converting the pixel to our
			// desired format.
			if a == 0 {
				// Handle fully-transparent pixels quickly.
				// Color correction is irrelevant here.
				im.Pix[j*im.Stride+4*i+0] = 0
				im.Pix[j*im.Stride+4*i+1] = 0
				im.Pix[j*im.Stride+4*i+2] = 0
				im.Pix[j*im.Stride+4*i+3] = 0
			} else if fp.inputCCF == nil {
				// No color correction
				im.Pix[j*im.Stride+4*i+0] = float32(r) / 65535.0
				im.Pix[j*im.Stride+4*i+1] = float32(g) / 65535.0
				im.Pix[j*im.Stride+4*i+2] = float32(b) / 65535.0
				im.Pix[j*im.Stride+4*i+3] = float32(a) / 65535.0
			} else if a == 65535 {
				// Fast path for fully-opaque pixels
				clr[0] = float64(r) / 65535.0
				clr[1] = float64(g) / 65535.0
				clr[2] = float64(b) / 65535.0
				// Convert to linear color.
				fp.inputCCF(clr[0:3])
				// Store
				im.Pix[j*im.Stride+4*i+0] = float32(clr[0])
				im.Pix[j*im.Stride+4*i+1] = float32(clr[1])
				im.Pix[j*im.Stride+4*i+2] = float32(clr[2])
				im.Pix[j*im.Stride+4*i+3] = 1.0
			} else {
				// Partial transparency, with color correction.
				// Convert to floating point.
				clr[0] = float64(r) / 65535.0
				clr[1] = float64(g) / 65535.0
				clr[2] = float64(b) / 65535.0
				clr[3] = float64(a) / 65535.0
				// Convert to unassociated alpha, so that we can do color conversion.
				clr[0] /= clr[3]
				clr[1] /= clr[3]
				clr[2] /= clr[3]
				// Convert to linear color.
				fp.inputCCF(clr[0:3])
				// Convert back to associated alpha, and store.
				im.Pix[j*im.Stride+4*i+0] = float32(clr[0] * clr[3])
				im.Pix[j*im.Stride+4*i+1] = float32(clr[1] * clr[3])
				im.Pix[j*im.Stride+4*i+2] = float32(clr[2] * clr[3])
				im.Pix[j*im.Stride+4*i+3] = float32(clr[3])
			}
		}
	}
	return nil
}

// Convert from: linear colorspace, associated alpha
//           to: target colorspace, associated alpha, samples clamped to [0,1]
func (fp *FPObject) convertDstFPImage(im *FPImage) {
	var i, j, k int
	var clr [4]float64

	for j = 0; j < (im.Rect.Max.Y - im.Rect.Min.Y); j++ {
		for i = 0; i < (im.Rect.Max.X - im.Rect.Min.X); i++ {
			if !fp.hasTransparency {
				clr[3] = 1.0
			} else {
				clr[3] = float64(im.Pix[j*im.Stride+i*4+3]) // alpha value
				if clr[3] <= 0.0 {                          // Fully transparent
					im.Pix[j*im.Stride+i*4+0] = 0.0
					im.Pix[j*im.Stride+i*4+1] = 0.0
					im.Pix[j*im.Stride+i*4+2] = 0.0
					im.Pix[j*im.Stride+i*4+3] = 0.0
					continue
				}
				if clr[3] > 1.0 {
					clr[3] = 1.0
				}
			}
			clr[0] = float64(im.Pix[j*im.Stride+i*4+0])
			clr[1] = float64(im.Pix[j*im.Stride+i*4+1])
			clr[2] = float64(im.Pix[j*im.Stride+i*4+2])
			// Clamp to [0,1], and convert to unassociated alpha
			for k = 0; k < 3; k++ {
				if fp.hasTransparency {
					clr[k] /= clr[3]
				}
				if clr[k] < 0.0 {
					clr[k] = 0.0
				}
				if clr[k] > 1.0 {
					clr[k] = 1.0
				}
			}
			// Convert from linear color
			if fp.outputCCF != nil {
				fp.outputCCF(clr[0:3])
			}
			// Overwrite the old value (leave it as unassociated alpha)
			im.Pix[j*im.Stride+i*4+0] = float32(clr[0])
			im.Pix[j*im.Stride+i*4+1] = float32(clr[1])
			im.Pix[j*im.Stride+i*4+2] = float32(clr[2])
			im.Pix[j*im.Stride+i*4+3] = float32(clr[3])
		}
	}
}

// Tell fpresize the image to read.
// This may or may not make an internal copy of the image -- no promises are
// made. The source image must remain valid and unchanged until the caller is done
// calling Resize.
// Color correction settings must be established before calling SetSourceImage.
// Only one source image is allowed per FPObject (though multiple target images
// may be made from it.)
func (fp *FPObject) SetSourceImage(srcImg image.Image) {
	fp.srcImage = srcImg
	fp.srcBounds = srcImg.Bounds()
	fp.srcW = fp.srcBounds.Max.X - fp.srcBounds.Min.X
	fp.srcH = fp.srcBounds.Max.Y - fp.srcBounds.Min.Y
}

func (fp *FPObject) SetFilterGetter(gff FilterGetter) {
	fp.filterGetter = gff
}

// SetFilter sets the Filter to use when resizing.
// This should be something returned by a Make*Filter function, or a custom
// filter.
// If not called, a reasonable default will be used (currently Lanczos-2).
func (fp *FPObject) SetFilter(fpf *Filter) {
	fp.SetFilterGetter(func(isVertical bool, scaleFactor float64) *Filter { return fpf })
}

func (fp *FPObject) SetBlurGetter(gbf BlurGetter) {
	fp.blurGetter = gbf
}

// SetBlur changes the amount of blurring done when resizing.
// The default is 1.0. Larger values blur more.
func (fp *FPObject) SetBlur(blur float64) {
	fp.blurGetter = func(isVertical bool, scaleFactor float64) float64 {
		return blur
	}
}

// The standard input ColorConverter.
func SRGBToLinear(s []float64) {
	for k := range s {
		if s[k] <= 0.0404482362771082 {
			s[k] /= 12.92
		} else {
			s[k] = math.Pow((s[k]+0.055)/1.055, 2.4)
		}
	}
}

// The standard output ColorConverter.
func LinearTosRGB(s []float64) {
	for k := range s {
		if s[k] <= 0.00313066844250063 {
			s[k] *= 12.92
		} else {
			s[k] = 1.055*math.Pow(s[k], 1.0/2.4) - 0.055
		}
	}
}

// Supply a ColorConverter function to use when converting the original colors
// to the colors in which the resizing will be performed.
// 
// This must be called before calling SetSourceImage().
// The default value is SRGBToLinear.
// This may be nil, for no conversion.
func (fp *FPObject) SetInputColorConverter(ccf ColorConverter) {
	fp.inputCCF = ccf
	fp.inputCCFSet = true
}

// Supply a ColorConverter function to use when converting from the colors in
// which the resizing was performed, to the final colors.
// 
// The default value is LinearTosRGB.
// This may be nil, for no conversion.
func (fp *FPObject) SetOutputColorConverter(ccf ColorConverter) {
	fp.outputCCF = ccf
	fp.outputCCFSet = true
}

// Set the size and origin of the resized image.
func (fp *FPObject) SetTargetBounds(dstBounds image.Rectangle) {
	fp.dstBounds = dstBounds
	fp.dstW = fp.dstBounds.Max.X - fp.dstBounds.Min.X
	fp.dstH = fp.dstBounds.Max.Y - fp.dstBounds.Min.Y
}

// Resize performs the resize, and returns a pointer to an image that uses the
// custom FPImage type.
func (fp *FPObject) Resize() (*FPImage, error) {
	var err error
	var intermedFPImage *FPImage // The image after resizing vertically

	if int64(fp.dstW)*int64(fp.dstH) > maxImagePixels {
		return nil, errors.New("Target image too large")
	}

	// Make sure color correction is set up.
	if !fp.inputCCFSet {
		// If the caller didn't set a color Converter, set it to sRGB.
		fp.SetInputColorConverter(SRGBToLinear)
	}
	if !fp.outputCCFSet {
		fp.SetOutputColorConverter(LinearTosRGB)
	}

	if fp.srcFPImage == nil {
		fp.srcFPImage = new(FPImage)
		err = fp.copySrcToFPImage(fp.srcFPImage)
		if err != nil {
			return nil, err
		}
	}

	intermedFPImage = new(FPImage)
	fp.resizeHeight(fp.srcFPImage, intermedFPImage, fp.dstH)

	fp.dstFPImage = new(FPImage)
	fp.resizeWidth(intermedFPImage, fp.dstFPImage, fp.dstW)
	fp.dstFPImage.Rect = fp.dstBounds

	fp.convertDstFPImage(fp.dstFPImage)

	return fp.dstFPImage, nil
}
