// ◄◄◄ fpresize.go ►►►
// Copyright © 2012 Jason Summers

// fpresize performs high-quality resizing of raster images.
package fpresize

import "image"
import "math"
import "fmt"
import "errors"
import "runtime"

// FPObject is an opaque struct that tracks the state of the resize operation.
// There usually should be one FPObject per source image.
type FPObject struct {
	srcImage   image.Image
	srcFPImage *FPImage
	srcBounds  image.Rectangle
	dstBounds  image.Rectangle
	srcW, srcH int
	dstW, dstH int

	hasTransparency bool // Does the source image have transparency?

	filterGetter FilterGetter
	blurGetter   BlurGetter

	inputCCFSet    bool
	inputCCF       ColorConverter
	inputCCFFlags  uint32
	outputCCFSet   bool
	outputCCF      ColorConverter
	outputCCFFlags uint32

	inputCCLookupTable16 *[65536]float32 // color conversion cache
	outputCCLookupTable8 []uint8
	outputCCTable8Size   int

	progressCallback func(msg string)

	numWorkers int // Number of worker goroutines we will use
	maxWorkers int // Max number requested by caller. 0 = not set.
}

// A ColorConverter is passed a slice of samples. It converts them all to
// a new colorspace, in-place.
// If CCFFlagWholePixels is set, the first sample is Red, then Green, Blue,
// Red, Green, Blue, etc.
type ColorConverter func(x []float32)

const (
	// If set via Set*ColorConverterFlags(), results from color conversion will
	// not be cached.
	CCFFlagNoCache = 0x00000001
	// If set via Set*ColorConverterFlags(), the R, G, and B channels may have
	// different response curves.
	CCFFlagWholePixels = 0x00000002
)

// A FilterGetter is a function that returns a Filter. The Filter
// returned can depend on which dimension is being resized, and
// other things.
type FilterGetter func(isVertical bool) *Filter

// A BlurGetter is a function that returns a 'blur' setting.
type BlurGetter func(isVertical bool) float64

type fpWeight struct {
	srcSamIdx int
	dstSamIdx int
	weight    float32
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
		reductionFactor *= fp.blurGetter(isVertical)
	}

	if fp.filterGetter != nil {
		filter = fp.filterGetter(isVertical)
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
			weightList[weightsUsed].srcSamIdx = srcSamIdx
			weightList[weightsUsed].dstSamIdx = dstSamIdx
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

type resampleWorkItem struct {
	// src* and dst* are references to a set of samples within (presumably) a FPImage object.
	// Sam[Stride*0] is the first sample; Sam[Stride*1] is the next, ...
	srcSam     []float32
	dstSam     []float32
	srcStride  int
	dstStride  int
	weightList []fpWeight
	stopNow    bool
}

// Read workItems (each representing a row or column to resample) from workQueue,
// and resample them.
func resampleWorker(workQueue chan resampleWorkItem) {
	var wi resampleWorkItem

	for {
		// Get next thing to do
		wi = <-workQueue
		if wi.stopNow {
			return
		}

		// resample1d(&wi)
		for i := range wi.weightList {
			// This is the line of code that actually does the resampling. (But All the
			// interesting things were precalculated in createWeightList().)
			wi.dstSam[wi.weightList[i].dstSamIdx*wi.dstStride] += wi.srcSam[wi.weightList[i].srcSamIdx*
				wi.srcStride] * wi.weightList[i].weight
		}
	}
}

// Create dst, an image with a different height than src.
// dst is a zeroed-out struct, created by the caller.
// resizeHeight sets its fields, and makes its origin (0,0).
func (fp *FPObject) resizeHeight(src *FPImage, dst *FPImage, dstH int) {
	var nSamples int
	var srcH int
	var w int // width of both images
	var wi resampleWorkItem
	var i int

	fp.progressMsgf("Changing height, %d -> %d", fp.srcH, fp.dstH)

	w = src.Rect.Max.X - src.Rect.Min.X
	srcH = src.Rect.Max.Y - src.Rect.Min.Y

	dst.Rect.Min.X = 0
	dst.Rect.Min.Y = 0
	dst.Rect.Max.X = w
	dst.Rect.Max.Y = dstH

	dst.Stride = w * 4
	nSamples = dst.Stride * dstH
	dst.Pix = make([]float32, nSamples)

	wi.weightList = fp.createWeightList(srcH, dstH, true)

	wi.srcStride = src.Stride
	wi.dstStride = dst.Stride

	workQueue := make(chan resampleWorkItem)

	// Start workers
	for i = 0; i < fp.numWorkers; i++ {
		go resampleWorker(workQueue)
	}

	// Iterate over the columns (of which src and dst have the same number)
	// Columns of *samples*, that is, not pixels.
	for col := 0; col < 4*w; col++ {
		if fp.hasTransparency || (col%4 != 3) { // If no transparency, skip over the alpha samples
			wi.srcSam = src.Pix[col:]
			wi.dstSam = dst.Pix[col:]
			// Assign the work to whatever worker happens to be available to receive it.
			// Note that this stuct is passed by value, so it's okay to modify it and
			// pass it again.
			workQueue <- wi
		}
	}

	// Tell the workers to stop, and block until they all receive our Stop message.
	// TODO: This seems kind of crude.
	wi.stopNow = true
	for i = 0; i < fp.numWorkers; i++ {
		workQueue <- wi
	}
}

// Create dst, an image with a different width than src.
// TODO: Maybe merge resizeWidth & resizeHeight
func (fp *FPObject) resizeWidth(src *FPImage, dst *FPImage, dstW int) {
	var nSamples int
	var srcW int
	var h int // height of both images
	var wi resampleWorkItem
	var i int

	fp.progressMsgf("Changing width, %d -> %d", fp.srcW, fp.dstW)

	srcW = src.Rect.Max.X - src.Rect.Min.X
	h = src.Rect.Max.Y - src.Rect.Min.Y

	dst.Rect.Min.X = 0
	dst.Rect.Min.Y = 0
	dst.Rect.Max.X = dstW
	dst.Rect.Max.Y = h
	dst.Stride = dstW * 4
	nSamples = dst.Stride * h
	dst.Pix = make([]float32, nSamples)

	wi.weightList = fp.createWeightList(srcW, dstW, false)

	wi.srcStride = 4
	wi.dstStride = 4

	workQueue := make(chan resampleWorkItem)

	for i = 0; i < fp.numWorkers; i++ {
		go resampleWorker(workQueue)
	}

	// Iterate over the rows (of which src and dst have the same number)
	for row := 0; row < h; row++ {
		// Iterate over R,G,B,A
		for k := 0; k < 4; k++ {
			if fp.hasTransparency || k != 3 {
				wi.srcSam = src.Pix[row*src.Stride+k:]
				wi.dstSam = dst.Pix[row*dst.Stride+k:]
				workQueue <- wi
			}
		}
	}

	wi.stopNow = true
	for i = 0; i < fp.numWorkers; i++ {
		workQueue <- wi
	}
}

// Take an image fresh from resizeWidth/resizeHeight
//  * associated alpha, linear colorspace, alpha samples may not be valid
// Convert to 
//  * unassociated alpha, linear colorspace, alpha samples always valid,
//    all samples clamped to [0,1].
//
// This extra pass over the image may seem wasteful, but it helps to keep the
// code clean. The image data will be handed to one of several routines after
// this, so this helps to prevent duplication of some tedious code.
//
// It is possible that we will convert to unassociated alpha needlessly, only
// to convert right back to associated alpha. That will happen if color
// correction is disabled, and the final image format uses associated alpha.
// We deem that to be not worth optimizing for.
func (fp *FPObject) postProcessImage(im *FPImage) {
	var k int

	for j := 0; j < (im.Rect.Max.Y - im.Rect.Min.Y); j++ {
		for i := 0; i < (im.Rect.Max.X - im.Rect.Min.X); i++ {
			rp := j*im.Stride + i*4 // index of the Red sample in im.Pix
			ap := rp + 3            // index of the alpha sample

			if !fp.hasTransparency {
				// This image is known to have no transparency. Set alpha to 1,
				// and clamp the other samples to [0,1]
				im.Pix[ap] = 1.0
				for k = 0; k < 3; k++ {
					if im.Pix[rp+k] < 0.0 {
						im.Pix[rp+k] = 0.0
					} else if im.Pix[rp+k] > 1.0 {
						im.Pix[rp+k] = 1.0
					}
				}
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
}

// Tell fpresize the image to read.
// Only one source image may be selected per FPObject.
// Once selected, the image may not be changed until after the last Resize
// method is called.
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
	fp.SetFilterGetter(func(isVertical bool) *Filter { return fpf })
}

func (fp *FPObject) SetBlurGetter(gbf BlurGetter) {
	fp.blurGetter = gbf
}

// SetBlur changes the amount of blurring done when resizing.
// The default is 1.0. Larger values blur more.
func (fp *FPObject) SetBlur(blur float64) {
	fp.blurGetter = func(isVertical bool) float64 {
		return blur
	}
}

// Returns the current scale factor (target size divided by source size)
// for the given dimension.
// This is only valid during or after Resize() -- it's meant to be used by
// callback functions, so that the filter to use could be selected based on
// this information.
func (fp *FPObject) ScaleFactor(isVertical bool) float64 {
	if isVertical {
		return float64(fp.dstH) / float64(fp.srcH)
	}
	return float64(fp.dstW) / float64(fp.srcW)
}

// Returns true if the source image has any pixels that are not fully opaque,
// or transparency is otherwise needed to process the image.
// This is only valid during or after Resize() -- it's meant to be used by
// callback functions, so that the filter to use could be selected based on
// this information.
func (fp *FPObject) HasTransparency() bool {
	return fp.hasTransparency
}

// The standard input ColorConverter.
func SRGBToLinear(s []float32) {
	for k := range s {
		if s[k] <= 0.0404482362771082 {
			s[k] /= 12.92
		} else {
			s[k] = float32(math.Pow(float64((s[k]+0.055)/1.055), 2.4))
		}
	}
}

// The standard output ColorConverter.
func LinearTosRGB(s []float32) {
	for k := range s {
		if s[k] <= 0.00313066844250063 {
			s[k] *= 12.92
		} else {
			s[k] = float32(1.055*math.Pow(float64(s[k]), 1.0/2.4) - 0.055)
		}
	}
}

// Supply a ColorConverter function to use when converting the original colors
// to the colors in which the resizing will be performed.
// 
// This must be called before calling a Resize method, and may not be changed
// afterward.
// The default value is SRGBToLinear.
// This may be nil, for no conversion.
func (fp *FPObject) SetInputColorConverter(ccf ColorConverter) {
	fp.inputCCF = ccf
	fp.inputCCFSet = true
}

// Supply a ColorConverter function to use when converting from the colors in
// which the resizing was performed, to the final colors.
// 
// This may be called at any time, and remains in effect for future Resize
// method calls until it is called again.
// The default value is LinearTosRGB.
// This may be nil, for no conversion.
func (fp *FPObject) SetOutputColorConverter(ccf ColorConverter) {
	fp.outputCCF = ccf
	fp.outputCCFSet = true
}

// Accepts a bitwise combination of CCFFlag* values.
func (fp *FPObject) SetInputColorConverterFlags(flags uint32) {
	fp.inputCCFFlags = flags
}

// Accepts a bitwise combination of CCFFlag* values.
func (fp *FPObject) SetOutputColorConverterFlags(flags uint32) {
	fp.outputCCFFlags = flags
}

// Set the size and origin of the resized image.
func (fp *FPObject) SetTargetBounds(dstBounds image.Rectangle) {
	fp.dstBounds = dstBounds
	fp.dstW = fp.dstBounds.Max.X - fp.dstBounds.Min.X
	fp.dstH = fp.dstBounds.Max.Y - fp.dstBounds.Min.Y
}

func (fp *FPObject) SetProgressCallback(fn func(msg string)) {
	fp.progressCallback = fn
}

func (fp *FPObject) progressMsgf(format string, a ...interface{}) {
	if fp.progressCallback == nil {
		return
	}
	msg := fmt.Sprintf(format, a...)
	fp.progressCallback(msg)
}

func (fp *FPObject) SetMaxWorkerThreads(n int) {
	fp.maxWorkers = n
}

func (fp *FPObject) resizeMain() (*FPImage, error) {
	var err error
	var intermedFPImage *FPImage // The image after resizing vertically
	var dstFPImage *FPImage

	fp.numWorkers = runtime.GOMAXPROCS(0)
	if fp.numWorkers < 1 {
		fp.numWorkers = 1
	}
	if fp.maxWorkers > 0 && fp.numWorkers > fp.maxWorkers {
		fp.numWorkers = fp.maxWorkers
	}

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

	// When changing the width, the relevant samples are close together in memory.
	// When changing the height, they are much farther apart. On a modern computer,
	// due to caching, that makes changing the width much faster than the height.
	// So it is beneficial to resize the height first if we are increasing the
	// image size, and the width first if we are reducing it.
	if fp.dstW > fp.srcW {
		intermedFPImage = new(FPImage)
		fp.resizeHeight(fp.srcFPImage, intermedFPImage, fp.dstH)

		dstFPImage = new(FPImage)
		fp.resizeWidth(intermedFPImage, dstFPImage, fp.dstW)
	} else {
		intermedFPImage = new(FPImage)
		fp.resizeWidth(fp.srcFPImage, intermedFPImage, fp.dstW)

		dstFPImage = new(FPImage)
		fp.resizeHeight(intermedFPImage, dstFPImage, fp.dstH)
	}

	fp.progressMsgf("Post-processing image")
	fp.postProcessImage(dstFPImage)

	dstFPImage.Rect = fp.dstBounds

	return dstFPImage, nil
}

// Resize performs the resize, and returns a pointer to an image that
// uses the custom FPImage type.
//
// This function will be deprecated or removed, in favor of specific functions
// like ResizeToNRGBA.
func (fp *FPObject) Resize() (*FPImage, error) {

	dstFPImage, err := fp.resizeMain()
	if err != nil {
		return nil, err
	}

	fp.convertDstFPImage(dstFPImage)
	return dstFPImage, nil
}

// ResizeNRGBA performs the resize, and returns a pointer to an image that
// uses the NRGBA format.
func (fp *FPObject) ResizeToNRGBA() (*image.NRGBA, error) {

	dstFPImage, err := fp.resizeMain()
	if err != nil {
		return nil, err
	}

	nrgba := fp.convertDstFPImageToNRGBA(dstFPImage)
	return nrgba, nil
}

// ResizeNRGBA performs the resize, and returns a pointer to an image that
// uses the NRGBA64 format.
func (fp *FPObject) ResizeToNRGBA64() (*image.NRGBA64, error) {

	dstFPImage, err := fp.resizeMain()
	if err != nil {
		return nil, err
	}

	// TODO: Optimize this
	fp.convertDstFPImage(dstFPImage)
	nrgba64 := dstFPImage.copyToNRGBA64()
	return nrgba64, nil
}
