// ◄◄◄ fpresize.go ►►►
// Copyright © 2012 Jason Summers

package fpresize

// This is the main file of the fpresize library.
// It implements the resize algorithm, and most of the API.

import "image"
import "math"
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

	srcHasTransparency      bool // Does the source image have transparency?
	srcHasColor             bool // Is the source image NOT grayscale (or gray+alpha)?
	mustProcessTransparency bool // Do we need to process an alpha channel?
	mustProcessColor        bool // Might any of the color channels differ?

	filterGetter FilterGetter
	blurGetter   BlurGetter

	inputCCFSet    bool
	inputCCF       ColorConverter
	inputCCFFlags  uint32
	outputCCFSet   bool
	outputCCF      ColorConverter
	outputCCFFlags uint32

	virtualPixels int // A virtPix* constant

	progressCallback func(format string, a ...interface{})

	numWorkers int // Number of worker goroutines we will use
	maxWorkers int // Max number requested by caller. 0 = not set.

	channelInfo [4]channelInfoType
}

type channelInfoType struct {
	// False if this channel doesn't need to be processed (e.g. the
	// alpha channel when the image is opaque).
	mustProcess bool
}

const (
	VirtualPixelsNone = iota
	VirtualPixelsTransparent
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

// Create and return a weightlist for the given dimension, using fp.filter.
func (fp *FPObject) createWeightList(isVertical bool) (weightList []fpWeight) {
	var filter *Filter
	var radius float64
	var filterFlags uint32
	var srcN, dstCanvasN int
	var srcN_flt float64
	var dstTrueN float64
	var dstOffset float64
	var scaleFactor float64
	var reductionFactor float64
	var weightsUsed int

	if isVertical {
		srcN, dstCanvasN = fp.srcH, fp.dstCanvasH
		dstTrueN = fp.dstTrueH
		dstOffset = fp.dstOffsetY
	} else {
		srcN, dstCanvasN = fp.srcW, fp.dstCanvasW
		dstTrueN = fp.dstTrueW
		dstOffset = fp.dstOffsetX
	}
	srcN_flt = float64(srcN)
	scaleFactor = dstTrueN / srcN_flt

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

	// Allocate a weight list, whose size is based on the maximum number of times
	// the nested loops below can execute.
	weightListCap := int(1.0 + (1.01+2.0*radius*reductionFactor)*float64(dstCanvasN))
	weightList = make([]fpWeight, weightListCap)

	for dstSamIdx := 0; dstSamIdx < dstCanvasN; dstSamIdx++ {
		var v_norm float64 // Sum of the filter values for the current sample
		var v_count int    // Number of weights used by the current sample

		// Figure out the range of src samples that are relevent to this dst sample.
		posInSrc := ((0.5+float64(dstSamIdx)-dstOffset)/dstTrueN)*srcN_flt - 0.5
		firstSrcSamIdx := int(math.Ceil(posInSrc - radius*reductionFactor - 0.0001))
		lastSrcSamIdx := int(math.Floor(posInSrc + radius*reductionFactor + 0.0001))

		v_norm = 0.0
		v_count = 0

		// Iterate through the input samples that affect this output sample
		for srcSamIdx := firstSrcSamIdx; srcSamIdx <= lastSrcSamIdx; srcSamIdx++ {
			var isVirtual bool

			if srcSamIdx >= 0 && srcSamIdx < srcN {
				isVirtual = false
			} else {
				if fp.virtualPixels == VirtualPixelsNone {
					continue
				}
				isVirtual = true
			}

			// arg is the value passed to the filter function;
			// v is the value returned by the filter function.
			arg := (float64(srcSamIdx) - posInSrc) / reductionFactor
			// For convenience, (usually) don't supply negative arguments to filters.
			if (arg < 0.0) && (filterFlags&FilterFlagAsymmetric == 0) {
				arg = -arg
			}

			v := filter.F(arg, scaleFactor)
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

		if math.Abs(v_norm) < 0.000001 {
			// This shouldn't happen (with a sane filter), but just to protect
			// against division-by-zero...
			v_norm = 0.000001
		}

		// Normalize the weights we just added
		for w := weightsUsed - v_count; w < weightsUsed; w++ {
			weightList[w].weight /= float32(v_norm)
		}
	}

	weightList = weightList[:weightsUsed] // Re-slice, to set len(weightList)
	return
}

// Data that is constant for all workers.
type resampleWorkContext struct {
	weightList []fpWeight
	srcStride  int
	dstStride  int
}

type resampleWorkItem struct {
	// src* and dst* are references to a set of samples within (presumably) an FPImage object.
	// Sam[wc.Stride*0] is the first sample; Sam[wc.Stride*1] is the next, ...
	srcSam  []float32
	dstSam  []float32
	stopNow bool
}

// Read workItems (each representing a row or column to resample) from workQueue,
// and resample them.
func resampleWorker(wc *resampleWorkContext, workQueue chan resampleWorkItem) {
	var wi resampleWorkItem

	for {
		wi = <-workQueue // Get next thing to do.

		if wi.stopNow {
			return
		}

		// Resample one row or column.
		for i := range wc.weightList {
			if wc.weightList[i].srcSamIdx >= 0 {
				// Not a (transparent) virtual pixel
				wi.dstSam[wc.weightList[i].dstSamIdx*wc.dstStride] += wi.srcSam[wc.weightList[i].srcSamIdx*
					wc.srcStride] * wc.weightList[i].weight
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

	wc := new(resampleWorkContext)
	dst = new(FPImage)

	w = src.Rect.Dx()

	dst.Rect.Min.X = 0
	dst.Rect.Min.Y = 0
	dst.Rect.Max.X = w
	dst.Rect.Max.Y = fp.dstCanvasH

	dst.Stride = w * 4
	nSamples = dst.Stride * fp.dstCanvasH
	dst.Pix = make([]float32, nSamples)

	wc.weightList = fp.createWeightList(true)

	wc.srcStride = src.Stride
	wc.dstStride = dst.Stride

	workQueue := make(chan resampleWorkItem)

	// Start workers
	for i = 0; i < fp.numWorkers; i++ {
		go resampleWorker(wc, workQueue)
	}

	// Iterate over the columns (of which src and dst have the same number).
	// Columns of *samples*, that is, not pixels.
	for col := 0; col < 4*w; col++ {
		if fp.channelInfo[col%4].mustProcess {
			wi.srcSam = src.Pix[col:]
			wi.dstSam = dst.Pix[col:]
			// Assign the work to whatever worker happens to be available to receive it.
			// Note that this struct is passed by value, so it's okay to modify it and
			// pass it again.
			workQueue <- wi
		}
	}

	// Tell the workers to stop, and block until they all receive our Stop message.
	wi.stopNow = true
	for i = 0; i < fp.numWorkers; i++ {
		workQueue <- wi
	}
	return
}

// Create dst, an image with a different width than src.
func (fp *FPObject) resizeWidth(src *FPImage) (dst *FPImage) {
	var nSamples int
	var h int // height of both images
	var wi resampleWorkItem
	var i int

	fp.progressMsgf("Changing width, %d -> %d", fp.srcW, fp.dstCanvasW)

	wc := new(resampleWorkContext)
	dst = new(FPImage)

	h = src.Rect.Dy()

	dst.Rect.Min.X = 0
	dst.Rect.Min.Y = 0
	dst.Rect.Max.X = fp.dstCanvasW
	dst.Rect.Max.Y = h
	dst.Stride = fp.dstCanvasW * 4
	nSamples = dst.Stride * h
	dst.Pix = make([]float32, nSamples)

	wc.weightList = fp.createWeightList(false)

	wc.srcStride = 4
	wc.dstStride = 4

	workQueue := make(chan resampleWorkItem)

	for i = 0; i < fp.numWorkers; i++ {
		go resampleWorker(wc, workQueue)
	}

	// Iterate over the rows (of which src and dst have the same number)
	for row := 0; row < h; row++ {
		// Iterate over R,G,B,A
		for k := 0; k < 4; k++ {
			if fp.channelInfo[k].mustProcess {
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

// New allocates a new FPObject, and sets its source image. This
// is equivalent to calling new(FPObject) followed by SetSourceImage.
func New(srcImg image.Image) *FPObject {
	fp := new(FPObject)
	fp.SetSourceImage(srcImg)
	return fp
}

// SetSourceImage tells fpresize the image to read.
// Only one source image may be selected per FPObject.
// Once selected, the caller may not modify the image until after the first
// successful call to a Resize* method.
//
// It is recommended to call New(), instead of calling SetSourceImage
// directly.
func (fp *FPObject) SetSourceImage(srcImg image.Image) {
	fp.srcImage = srcImg
	fp.srcBounds = srcImg.Bounds()
	fp.srcW = fp.srcBounds.Dx()
	fp.srcH = fp.srcBounds.Dy()
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
	return fp.mustProcessTransparency
}

// If HasColor returns false, the image is grayscale (with or without
// transparency). A return value of true does not necessarily provide any
// information -- the image could still be grayscale.
//
// This is only valid during or after Resize() -- it's meant to be used by
// callback functions, so that the filter to use could be selected based on
// this information.
func (fp *FPObject) HasColor() bool {
	return fp.mustProcessColor
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
// This must be called before calling the first Resize method, and may not be
// changed afterward.
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

// Set the properties of the input color converter.
// Accepts a bitwise combination of CCFFlag* values.
func (fp *FPObject) SetInputColorConverterFlags(flags uint32) {
	fp.inputCCFFlags = flags
}

// Set the properties of the ouput color converter.
// Accepts a bitwise combination of CCFFlag* values.
func (fp *FPObject) SetOutputColorConverterFlags(flags uint32) {
	fp.outputCCFFlags = flags
}

// This sets the target image bounds to match the canvas bounds, so if
// that's not the case, the image bounds need to be set after calling this
// method, not before.
func (fp *FPObject) setTargetCanvasBounds(dstBounds image.Rectangle) {
	fp.dstBounds = dstBounds
	if fp.dstBounds.Max.X < fp.dstBounds.Min.X+1 {
		fp.dstBounds.Max.X = fp.dstBounds.Min.X + 1
	}
	if fp.dstBounds.Max.Y < fp.dstBounds.Min.Y+1 {
		fp.dstBounds.Max.Y = fp.dstBounds.Min.Y + 1
	}
	fp.dstCanvasW = fp.dstBounds.Dx()
	fp.dstCanvasH = fp.dstBounds.Dy()
	fp.dstOffsetX = 0.0
	fp.dstOffsetY = 0.0
	fp.dstTrueW = float64(fp.dstCanvasW)
	fp.dstTrueH = float64(fp.dstCanvasH)
}

// SetTargetBounds sets the size and origin of the resized image.
// The source image will be mapped onto the given bounds.
// It also sets the VirtualPixels setting to None.
//
// If the height or width is less than 1, the bounds will be adjusted
// so that it is 1.
func (fp *FPObject) SetTargetBounds(dstBounds image.Rectangle) {
	fp.setTargetCanvasBounds(dstBounds)
	fp.virtualPixels = VirtualPixelsNone
}

// SetTargetBoundsAdvanced sets the bounds of the target image, and
// the mapping of the source image onto it.
// It also sets the VirtualPixels setting to Transparent.
//
// dstBounds is the bounds of the target image.
//
// x1, y1: The point (in the same coordinate system as dstBounds) onto which
// the upper-left corner of the source image will be mapped. Note that it
// does not have to be an integer.
//
// x2, y2: The point onto which the lower-right corner of the source
// image will be mapped.
func (fp *FPObject) SetTargetBoundsAdvanced(dstBounds image.Rectangle,
	x1, y1, x2, y2 float64) {
	fp.setTargetCanvasBounds(dstBounds)
	fp.dstOffsetX = x1 - float64(fp.dstBounds.Min.X)
	fp.dstOffsetY = y1 - float64(fp.dstBounds.Min.Y)
	fp.dstTrueW = x2 - x1
	fp.dstTrueH = y2 - y1
	fp.virtualPixels = VirtualPixelsTransparent
}

// SetVirtualPixels controls how the edges of the image are handled.
// n is VirtualPixelsNone or VirtualPixelsTransparent.
// This can only be called after setting the target bounds.
func (fp *FPObject) SetVirtualPixels(n int) {
	fp.virtualPixels = n
}

// (This is a debugging method. Please don't use.)
func (fp *FPObject) SetProgressCallback(fn func(format string, a ...interface{})) {
	fp.progressCallback = fn
}

func (fp *FPObject) progressMsgf(format string, a ...interface{}) {
	if fp.progressCallback == nil {
		return
	}
	fp.progressCallback(format, a...)
}

// SetMaxWorkerThreads tells fpresize the maximum number of goroutines that it
// should use simultaneously to do image processing. 0 means default.
//
// In theory, there should be no reason to call this method, unless you want
// to slow down fpresize to conserve resources for other processes.
// In reality, though, there are situations in which reducing the number of
// goroutines will improve performance.
func (fp *FPObject) SetMaxWorkerThreads(n int) {
	fp.maxWorkers = n
}

func (fp *FPObject) resizeMain() (*FPImage, error) {
	var err error
	var intermedFPImage *FPImage
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
		err = fp.convertSrc(fp.srcImage, fp.srcFPImage)
		if err != nil {
			return nil, err
		}

		// Now that srcImage has been converted to srcFPImage, we don't need
		// it anymore.
		fp.srcImage = nil
	}

	fp.mustProcessTransparency = (fp.srcHasTransparency || fp.virtualPixels == VirtualPixelsTransparent)
	fp.mustProcessColor = fp.srcHasColor

	// Set the .channelInfo fields
	for k := 0; k < 4; k++ {
		if k == 3 {
			fp.channelInfo[k].mustProcess = fp.mustProcessTransparency
		} else if k == 1 || k == 2 {
			fp.channelInfo[k].mustProcess = fp.mustProcessColor
		} else {
			fp.channelInfo[k].mustProcess = true
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
// This method may be slow. You should almost always use ResizeToImage,
// ResizeToNRGBA, ResizeToRGBA, ResizeToNRGBA64, or ResizeToRGBA64 instead.
func (fp *FPObject) Resize() (*FPImage, error) {
	dstFPImage, err := fp.resizeMain()
	if err != nil {
		return nil, err
	}

	fp.convertDst_FP(dstFPImage)
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

	nrgba := fp.convertDst_NRGBA(dstFPImage)
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

	rgba := fp.convertDst_RGBA(dstFPImage)
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

	nrgba64 := fp.convertDst_NRGBA64(dstFPImage)
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

	rgba64 := fp.convertDst_RGBA64(dstFPImage)
	return rgba64, nil
}

const (
	// Indicates that you prefer grayscale images to be returned in image.Gray
	// or image.Gray16 format.
	ResizeFlagGrayOK = 0x00000001
	// Indicates that you prefer NRGBA(64) format to RGBA(64) format.
	ResizeFlagUnassocAlpha = 0x00000002
	// Indicates that you prefer 16-bit images ([N]RGBA64/Gray16 to [N]RGBA/Gray).
	ResizeFlag16Bit = 0x00000004
)

// ResizeToImage resize the image and returns an image.Image interface whose
// underlying type may vary depending on the source image type, and other
// things. The logic to use is determined by the 'flags' parameter.
//
// 'flags' is a bitwise combination of ResizeFlag* constants.
func (fp *FPObject) ResizeToImage(flags uint32) (image.Image, error) {
	var err error

	dstFPImage, err := fp.resizeMain()
	if err != nil {
		return nil, err
	}

	if !fp.mustProcessColor && !fp.mustProcessTransparency && flags&ResizeFlagGrayOK != 0 {
		if flags&ResizeFlag16Bit != 0 {
			gray16 := fp.convertDst_Gray16(dstFPImage)
			return gray16, nil
		}
		gray := fp.convertDst_Gray(dstFPImage)
		return gray, nil
	}

	if (flags & ResizeFlagUnassocAlpha) != 0 {
		if flags&ResizeFlag16Bit != 0 {
			nrgba64 := fp.convertDst_NRGBA64(dstFPImage)
			return nrgba64, nil
		}
		nrgba := fp.convertDst_NRGBA(dstFPImage)
		return nrgba, nil
	}

	if flags&ResizeFlag16Bit != 0 {
		rgba64 := fp.convertDst_RGBA64(dstFPImage)
		return rgba64, nil
	}
	rgba := fp.convertDst_RGBA(dstFPImage)
	return rgba, nil
}
