// ◄◄◄ fpresize.go ►►►
// Copyright © 2012 Jason Summers

package fpresize

// This is the main file of the fpresize library.
// It implements the resize algorithm, and most of the API.

import "image"
import "math"
import "fmt"
import "errors"
import "runtime"

// FPObject is an opaque struct that tracks the state of the resize process.
// There is one FPObject per source image.
type FPObject struct {
	srcImage   image.Image
	srcBounds  image.Rectangle
	dstBounds  image.Rectangle
	srcW       int
	srcH       int
	dstCanvasW int
	dstCanvasH int
	// How many pixels the left edge of the image is to the right of the left
	// edge of the canvas.
	dstOffsetX float64
	// How many pixels the top edge of the image is below the top
	// edge of the canvas.
	dstOffsetY float64
	// Size of the target rectangle onto which the source image is mapped.
	dstTrueW float64
	dstTrueH float64

	// Source image in FP format. This is recorded, so that it can be
	// resized multiple times.
	srcFPImage *FPImage

	hasTransparency bool // Does the source image have transparency?

	filterGetter FilterGetter
	blurGetter   BlurGetter

	inputCCFSet    bool
	inputCCF       ColorConverter
	inputCCFFlags  uint32
	outputCCFSet   bool
	outputCCF      ColorConverter
	outputCCFFlags uint32

	virtualPixels int // A virtPix* constant

	progressCallback func(msg string)

	numWorkers int // Number of worker goroutines we will use
	maxWorkers int // Max number requested by caller. 0 = not set.
}

const (
	VirtualPixelsNone        = 0
	VirtualPixelsTransparent = 1
)

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

// A FilterGetter is a function that returns a Filter. The isVertical
// parameter indicates the dimension for which the filter will be used.
// The filter could depend on other things as well; for example, the
// FilterGetter could call ScaleFactor().
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
func (fp *FPObject) createWeightList(isVertical bool) []fpWeight {
	var srcN, dstCanvasN int
	var dstTrueN float64
	var dstOffset float64
	if isVertical {
		srcN, dstCanvasN = fp.srcH, fp.dstCanvasH
		dstTrueN = fp.dstTrueH
		dstOffset = fp.dstOffsetY
	} else {
		srcN, dstCanvasN = fp.srcW, fp.dstCanvasW
		dstTrueN = fp.dstTrueW
		dstOffset = fp.dstOffsetX
	}
	var reductionFactor float64
	var radius float64
	var weightList []fpWeight
	var weightsUsed int
	var weightListCap int
	var srcN_flt float64 = float64(srcN)
	var scaleFactor float64 = dstTrueN / srcN_flt
	var filter *Filter
	var filterFlags uint32

	if dstTrueN < srcN_flt {
		reductionFactor = srcN_flt / dstTrueN
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
	weightListCap = int((1.01+2.0*radius*reductionFactor)*float64(dstCanvasN)) + 2
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
	var isVirtual bool
	var i int

	for dstSamIdx = 0; dstSamIdx < dstCanvasN; dstSamIdx++ {
		// Figure out the range of source samples that are relevent to this dst sample.
		posInSrc = ((0.5+float64(dstSamIdx)-dstOffset)/dstTrueN)*srcN_flt - 0.5
		firstSrcSamIdx = int(math.Ceil(posInSrc - radius*reductionFactor - 0.0001))
		lastSrcSamIdx = int(math.Floor(posInSrc + radius*reductionFactor + 0.0001))

		// Remember which item in the weightlist was the first one for this
		// target sample.
		idxOfFirstWeight = weightsUsed

		v_norm = 0.0
		v_count = 0

		// Iterate through the input samples that affect this output sample
		for srcSamIdx = firstSrcSamIdx; srcSamIdx <= lastSrcSamIdx; srcSamIdx++ {
			if srcSamIdx >= 0 && srcSamIdx < srcN {
				isVirtual = false
			} else {
				if fp.virtualPixels == VirtualPixelsNone {
					continue
				}
				isVirtual = true
			}

			arg = (float64(srcSamIdx) - posInSrc) / reductionFactor

			v = filter.F(fixupFilterArg(filterFlags, arg), scaleFactor)
			if v == 0.0 {
				continue
			}
			v_norm += v
			v_count++

			// Add this weight to the list (it will be normalized later)
			if isVirtual {
				weightList[weightsUsed].srcSamIdx = -1
				weightList[weightsUsed].dstSamIdx = -1
			} else {
				weightList[weightsUsed].srcSamIdx = srcSamIdx
				weightList[weightsUsed].dstSamIdx = dstSamIdx
			}
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

		for i := range wi.weightList {
			if wi.weightList[i].srcSamIdx >= 0 {
				// Not a (transparent) virtual pixel
				wi.dstSam[wi.weightList[i].dstSamIdx*wi.dstStride] += wi.srcSam[wi.weightList[i].srcSamIdx*
					wi.srcStride] * wi.weightList[i].weight
			}
		}
	}
}

// Create dst, an image with a different height than src.
// dst's origin will be (0,0).
func (fp *FPObject) resizeHeight(src *FPImage) (dst *FPImage) {
	var nSamples int
	var w int // width of both images
	var wi resampleWorkItem
	var i int

	fp.progressMsgf("Changing height, %d -> %d", fp.srcH, fp.dstCanvasH)

	dst = new(FPImage)

	w = src.Rect.Max.X - src.Rect.Min.X

	dst.Rect.Min.X = 0
	dst.Rect.Min.Y = 0
	dst.Rect.Max.X = w
	dst.Rect.Max.Y = fp.dstCanvasH

	dst.Stride = w * 4
	nSamples = dst.Stride * fp.dstCanvasH
	dst.Pix = make([]float32, nSamples)

	wi.weightList = fp.createWeightList(true)

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
	return
}

// Create dst, an image with a different width than src.
// TODO: Maybe merge resizeWidth & resizeHeight
func (fp *FPObject) resizeWidth(src *FPImage) (dst *FPImage) {
	var nSamples int
	var h int // height of both images
	var wi resampleWorkItem
	var i int

	fp.progressMsgf("Changing width, %d -> %d", fp.srcW, fp.dstCanvasW)

	dst = new(FPImage)

	h = src.Rect.Max.Y - src.Rect.Min.Y

	dst.Rect.Min.X = 0
	dst.Rect.Min.Y = 0
	dst.Rect.Max.X = fp.dstCanvasW
	dst.Rect.Max.Y = h
	dst.Stride = fp.dstCanvasW * 4
	nSamples = dst.Stride * h
	dst.Pix = make([]float32, nSamples)

	wi.weightList = fp.createWeightList(false)

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
	return
}

// SetSourceImage tells fpresize the image to read.
// Only one source image may be selected per FPObject.
// Once selected, the caller may not modify the image until after the first
// successful call to a Resize* method.
func (fp *FPObject) SetSourceImage(srcImg image.Image) {
	fp.srcImage = srcImg
	fp.srcBounds = srcImg.Bounds()
	fp.srcW = fp.srcBounds.Max.X - fp.srcBounds.Min.X
	fp.srcH = fp.srcBounds.Max.Y - fp.srcBounds.Min.Y
}

// SetFilterGetter specifies a function that will return the resampling filter
// to use. Said function will be called twice per resize: once per dimension.
func (fp *FPObject) SetFilterGetter(gff FilterGetter) {
	fp.filterGetter = gff
}

// SetFilter sets the resampling filter to use when resizing.
// This should be something returned by a Make*Filter function, or a custom
// filter.
// If not called, a reasonable default will be used (currently Lanczos-2).
func (fp *FPObject) SetFilter(fpf *Filter) {
	fp.SetFilterGetter(func(isVertical bool) *Filter { return fpf })
}

// SetBlurGetter specifies a function that will return the blur setting to
// use. Said function will be called twice per resize: once per dimension.
func (fp *FPObject) SetBlurGetter(gbf BlurGetter) {
	fp.blurGetter = gbf
}

// SetBlur sets the amount of blurring done when resizing.
// The default is 1.0. Larger values blur more.
func (fp *FPObject) SetBlur(blur float64) {
	fp.blurGetter = func(isVertical bool) float64 {
		return blur
	}
}

// ScaleFactor returns the current scale factor (target size divided by
// source size) for the given dimension.
// This is only valid during or after Resize() -- it's meant to be used by
// callback functions, so that the filter to use could be selected based on
// this information.
func (fp *FPObject) ScaleFactor(isVertical bool) float64 {
	if isVertical {
		return fp.dstTrueH / float64(fp.srcH)
	}
	return fp.dstTrueW / float64(fp.srcW)
}

// HasTransparency returns true if the source image has any pixels that are
// not fully opaque, or transparency is otherwise needed to process the image.
// This is only valid during or after Resize() -- it's meant to be used by
// callback functions, so that the filter to use could be selected based on
// this information.
func (fp *FPObject) HasTransparency() bool {
	return fp.hasTransparency
}

// SRGBToLinear is the default input ColorConverter.
func SRGBToLinear(s []float32) {
	for k := range s {
		if s[k] <= 0.0404482362771082 {
			s[k] /= 12.92
		} else {
			s[k] = float32(math.Pow(float64((s[k]+0.055)/1.055), 2.4))
		}
	}
}

// LinearTosRGB is the default output ColorConverter.
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

// SetTargetBounds sets the size and origin of the resized image.
// The source image will be mapped onto the given bounds.
// It also sets the VirtualPixels setting to None.
func (fp *FPObject) SetTargetBounds(dstBounds image.Rectangle) {
	fp.dstBounds = dstBounds
	fp.dstCanvasW = fp.dstBounds.Max.X - fp.dstBounds.Min.X
	fp.dstCanvasH = fp.dstBounds.Max.Y - fp.dstBounds.Min.Y
	fp.dstOffsetX = 0.0
	fp.dstOffsetY = 0.0
	fp.dstTrueW = float64(fp.dstCanvasW)
	fp.dstTrueH = float64(fp.dstCanvasH)
	fp.virtualPixels = VirtualPixelsNone
}

// SetTargetBoundsAdvanced sets the bounds of the target image, and
// the mapping of the source image onto it.
// It also sets the VirtualPixels setting to Transparent.
// 
// dstBounds is the bounds of the target image.
//
// x1, y1: The point onto which the upper-left corner of the source
// image will be mapped. Note that it does not have to be an integer.
//
// x2, y2: The point onto which the lower-left corner of the source
// image will be mapped.
func (fp *FPObject) SetTargetBoundsAdvanced(dstBounds image.Rectangle,
	x1, y1, x2, y2 float64) {
	fp.dstBounds = dstBounds
	fp.dstCanvasW = fp.dstBounds.Max.X - fp.dstBounds.Min.X
	fp.dstCanvasH = fp.dstBounds.Max.Y - fp.dstBounds.Min.Y
	fp.dstOffsetX = x1
	fp.dstOffsetY = y1
	fp.dstTrueW = x2 - x1
	fp.dstTrueH = y2 - y1
	fp.virtualPixels = VirtualPixelsTransparent
}

// SetVirtualPixels controls how the edges of the image are handled.
// This can only be called after setting the target bounds.
func (fp *FPObject) SetVirtualPixels(n int) {
	fp.virtualPixels = n
}

// (This is a debugging method. Please don't use.)
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

// SetMaxWorkerThreads tells fpresize the maximum number of goroutines that it
// should use simultaneously to do image processing. 0 means default.
//
// There should be no reason to call this method, unless you want to slow down
// fpresize to conserve resources for other routines.
//
// It will probably do no good to set this higher than your runtime.GOMAXPROCS
// setting, or higher than the number of available CPU cores.
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

	if int64(fp.dstCanvasW)*int64(fp.dstCanvasH) > maxImagePixels {
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
		err = fp.convertSrcToFP(fp.srcImage, fp.srcFPImage)
		if err != nil {
			return nil, err
		}

		// Now that srcImage has been converted to srcFPImage, we don't need
		// it anymore.
		fp.srcImage = nil

		// If we're using transparent virtual pixels, force processing of
		// the alpha channel.
		if fp.virtualPixels == VirtualPixelsTransparent {
			fp.hasTransparency = true
		}
	}

	// When changing the width, the relevant samples are close together in memory.
	// When changing the height, they are much farther apart. On a modern computer,
	// due to caching, that makes changing the width much faster than the height.
	// So it is beneficial to resize the height first if we are increasing the
	// image size, and the width first if we are reducing it.
	if fp.dstCanvasW > fp.srcW {
		intermedFPImage = fp.resizeHeight(fp.srcFPImage)
		dstFPImage = fp.resizeWidth(intermedFPImage)
	} else {
		intermedFPImage = fp.resizeWidth(fp.srcFPImage)
		dstFPImage = fp.resizeHeight(intermedFPImage)
	}

	dstFPImage.Rect = fp.dstBounds

	return dstFPImage, nil
}

// Resize resizes the image, and returns a pointer to an image that
// uses the custom FPImage type. The returned image is high-precision,
// and satisfies the image.Image and image/draw.Image interfaces.
//
// This method may be slow. You should almost always use ResizeToNRGBA,
// ResizeToRGBA, ResizeToNRGBA64, or ResizeToRGBA64 instead.
func (fp *FPObject) Resize() (*FPImage, error) {
	dstFPImage, err := fp.resizeMain()
	if err != nil {
		return nil, err
	}

	fp.convertFPToFinalFP(dstFPImage)
	return dstFPImage, nil
}

// ResizeNRGBA resizes the image, and returns a pointer to an image that
// uses the NRGBA format.
//
// Use this if you intend to write the image to an 8-bits-per-sample PNG
// file.
func (fp *FPObject) ResizeToNRGBA() (*image.NRGBA, error) {
	dstFPImage, err := fp.resizeMain()
	if err != nil {
		return nil, err
	}

	nrgba := fp.convertFPToNRGBA(dstFPImage)
	return nrgba, nil
}

// ResizeRGBA resizes the image, and returns a pointer to an image that
// uses the RGBA format.
//
// Use this if you intend to write the image to a JPEG file.
func (fp *FPObject) ResizeToRGBA() (*image.RGBA, error) {
	dstFPImage, err := fp.resizeMain()
	if err != nil {
		return nil, err
	}

	rgba := fp.convertFPToRGBA(dstFPImage)
	return rgba, nil
}

// ResizeNRGBA resizes the image, and returns a pointer to an image that
// uses the NRGBA64 format.
//
// Use this if you intend to write the image to a 16-bits-per-sample PNG
// file.
func (fp *FPObject) ResizeToNRGBA64() (*image.NRGBA64, error) {
	dstFPImage, err := fp.resizeMain()
	if err != nil {
		return nil, err
	}

	nrgba64 := fp.convertFPToNRGBA64(dstFPImage)
	return nrgba64, nil
}

// ResizeRGBA64 resizes the image, and returns a pointer to an image that
// uses the RGBA64 format.
//
// This is essentially the native format of Go's "image" package, so this
// may be the method to use if you are going to further process the image,
// instead of simply writing it to a file.
func (fp *FPObject) ResizeToRGBA64() (*image.RGBA64, error) {
	dstFPImage, err := fp.resizeMain()
	if err != nil {
		return nil, err
	}

	rgba64 := fp.convertFPToRGBA64(dstFPImage)
	return rgba64, nil
}
