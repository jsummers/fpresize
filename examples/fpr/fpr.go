// ◄◄◄ fpr.go ►►►
// Copyright © 2012 Jason Summers

package main

import "fmt"
import "os"
import "time"
import "flag"
import "path/filepath"
import "strings"
import "errors"
import "runtime"
import "image"
import "image/png"
import "image/jpeg"
import _ "image/gif"
import "github.com/jsummers/fpresize"

func readImageFromFile(srcFilename string) (image.Image, error) {
	var err error
	var srcImg image.Image

	file, err := os.Open(srcFilename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	srcImg, _, err = image.Decode(file)
	if err != nil {
		return nil, err
	}

	return srcImg, nil
}

func writeImageToFile(img image.Image, dstFilename string, outputFileFormat int) error {
	var err error

	file, err := os.Create(dstFilename)
	if err != nil {
		return err
	}
	defer file.Close()

	if outputFileFormat == ffJPEG {
		err = jpeg.Encode(file, img, nil)
	} else {
		err = png.Encode(file, img)
	}
	return err
}

var startTime time.Time
var processingStartTime time.Time
var processingStopTime time.Time
var lastMsgTime time.Time

func progressMsgf(options *options_type, format string, a ...interface{}) {
	if !options.verbose && !options.debug {
		return
	}
	msg := fmt.Sprintf(format, a...)
	now := time.Now()
	if options.debug {
		if !lastMsgTime.IsZero() {
			fmt.Printf("%v\n", now.Sub(lastMsgTime))
		}
	}
	fmt.Printf("%s\n", msg)
	lastMsgTime = now
}

const (
	ffUnknown = iota
	ffPNG     = iota
	ffJPEG    = iota
)

func getFileFormatByFilename(fn string) int {
	ext := strings.ToLower(filepath.Ext(fn))
	switch ext {
	case ".png":
		return ffPNG
	case ".jpg", ".jpeg":
		return ffJPEG
	}
	return ffUnknown
}

// An example of a custom filter.
// Return a filter that emulates a nearest-neighbor resize.
// Ties are broken in favor of the pixel to the right or bottom.
func makeNearestNeighborFilter() *fpresize.Filter {
	f := new(fpresize.Filter)
	f.F = func(x float64, scaleFactor float64) float64 {
		var n float64
		if scaleFactor < 1.0 {
			n = scaleFactor
		} else {
			n = 1.0
		}
		// Using exactly 0.5 would leave us at the mercy of floating point
		// roundoff error, possibly causing there to be 0 or 2 source pixels
		// within the filter's domain. For nearest-neighbor, there must always
		// be exactly 1. So we add a small fudge factor.
		if x >= -0.4999999*n && x <= 0.5000001*n {
			return 1.0
		}
		return 0.0
	}
	f.Radius = func(scaleFactor float64) float64 {
		// It's okay if Radius is a little larger than it needs to be,
		// but it's important that it not be too small.
		if scaleFactor < 1.0 {
			return scaleFactor/2.0 + 0.0001
		}
		return 0.5001
	}
	f.Flags = func(scaleFactor float64) uint32 {
		return fpresize.FilterFlagAsymmetric
	}
	return f
}

// Another custom filter example.
// Return a constant-valued box filter.
// Ties are broken arbitrarily -- pixels are not duplicated, split, or skipped.
// This is often identical to the "boxavg" filter, but it can be quite
// different sometimes, such as when reducing an image to exactly 2/3 its
// original size.
func makeBoxFilter() *fpresize.Filter {
	f := new(fpresize.Filter)
	f.F = func(x float64, scaleFactor float64) float64 {
		if x >= -0.4999999 && x <= 0.5000001 {
			return 1.0
		}
		return 0.0
	}
	f.Radius = func(scaleFactor float64) float64 {
		return 0.5001
	}
	f.Flags = func(scaleFactor float64) uint32 {
		return fpresize.FilterFlagAsymmetric
	}
	return f
}

func resizeMain(options *options_type) error {
	var err error
	var srcBounds image.Rectangle
	var resizedImage image.Image
	var srcImg image.Image
	var srcW, srcH, dstW, dstH int
	var outputFileFormat int
	var otherFlags uint32

	startTime := time.Now()

	// Allow more than one thread to be used by this process, if more than one CPU exists.
	runtime.GOMAXPROCS(runtime.NumCPU())

	outputFileFormat = getFileFormatByFilename(options.dstFilename)
	if outputFileFormat == ffUnknown {
		return errors.New("Can't determine output file format. Please name the output file to end in .png or .jpg")
	}

	progressMsgf(options, "Reading source file")
	srcImg, err = readImageFromFile(options.srcFilename)
	if err != nil {
		return err
	}

	// Also track the total time it takes to do the resize (i.e. don't count
	// the time it takes to read and write the files).
	processingStartTime = time.Now()

	fp := fpresize.New(srcImg)

	fp.SetProgressCallback(func(format string, a ...interface{}) {
		progressMsgf(options, format, a...)
	})

	if options.numThreads > 0 {
		fp.SetMaxWorkerThreads(options.numThreads)
	}

	if options.noGamma {
		// To do colorspace-unaware resizing, call the following methods:
		fp.SetInputColorConverter(nil)
		fp.SetOutputColorConverter(nil)
	}

	switch options.filterName {
	case "auto":
	case "lanczos2":
		fp.SetFilter(fpresize.MakeLanczosFilter(2))
	case "lanczos3", "lanczos":
		fp.SetFilter(fpresize.MakeLanczosFilter(3))
	case "mix":
		fp.SetFilter(fpresize.MakePixelMixingFilter())
	case "mitchell":
		fp.SetFilter(fpresize.MakeCubicFilter(1.0/3.0, 1.0/3.0))
	case "catrom":
		fp.SetFilter(fpresize.MakeCubicFilter(0.0, 0.5))
	case "hermite":
		fp.SetFilter(fpresize.MakeCubicFilter(0.0, 0.0))
	case "bspline":
		fp.SetFilter(fpresize.MakeCubicFilter(1.0, 0.0))
	case "gaussian":
		fp.SetFilter(fpresize.MakeGaussianFilter())
	case "triangle":
		fp.SetFilter(fpresize.MakeTriangleFilter())
	case "box":
		fp.SetFilter(makeBoxFilter())
	case "boxavg":
		fp.SetFilter(fpresize.MakeBoxAvgFilter())
	case "nearest":
		fp.SetFilter(makeNearestNeighborFilter())
	default:
		return fmt.Errorf("Unrecognized filter %+q", options.filterName)
	}

	// The filter to use can be different for the vertical and horizontal
	// dimensions. It can also depend on other available information, such
	// as the scale factor.
	// fp.SetFilterGetter(func(isVertical bool) *fpresize.Filter {
	//	if fp.ScaleFactor(isVertical) > 1.0 {
	//		return fpresize.MakeCubicFilter(1.0/3, 1.0/3)
	//	}
	//	return fpresize.MakeLanczosFilter(3)
	// })

	// To blur the image, call SetBlur(). Not all filters are suitable for blurring.
	// A Gaussian filter is a good choice.
	if options.blur != 1.0 {
		fp.SetBlur(options.blur)
	}

	// Decide the size of the resized image.
	srcBounds = srcImg.Bounds()
	srcW = srcBounds.Max.X - srcBounds.Min.X
	srcH = srcBounds.Max.Y - srcBounds.Min.Y
	if options.height > 0 && options.width > 0 {
		// Use the exact dimensions given
		dstW = options.width
		dstH = options.height
	} else if options.height > 0 {
		// Fit to height
		dstH = options.height
		dstW = int(0.5 + (float64(srcW)/float64(srcH))*float64(dstH))
	} else {
		// Fit to width
		dstW = options.width
		dstH = int(0.5 + (float64(srcH)/float64(srcW))*float64(dstW))
	}
	fp.SetTargetBounds(image.Rect(0, 0, dstW, dstH))

	if options.depth > 8 {
		otherFlags |= fpresize.ResizeFlag16Bit
	}

	// Do the resize.
	if outputFileFormat == ffPNG {
		resizedImage, err = fp.ResizeToImage(otherFlags | fpresize.ResizeFlagGrayOK | fpresize.ResizeFlagUnassocAlpha)
	} else if outputFileFormat == ffJPEG {
		// As of Go 1.0.3, the jpeg package does not support writing grayscale
		// images. Passing an image.Gray to it will only slow it down.
		// If and when the jpeg package supports grayscale, we should change
		// this to:
		//  resizedImage, err = fp.ResizeToImage(fpresize.ResizeFlagGrayOK)
		resizedImage, err = fp.ResizeToRGBA()
	} else {
		resizedImage, err = fp.ResizeToRGBA()
	}
	if err != nil {
		return err
	}

	processingStopTime = time.Now()

	progressMsgf(options, "Writing target file")
	err = writeImageToFile(resizedImage, options.dstFilename, outputFileFormat)
	if err != nil {
		return err
	}

	progressMsgf(options, "Done")
	if options.debug {
		fmt.Printf("Processing time: %v\n", processingStopTime.Sub(processingStartTime))
		fmt.Printf("Total time: %v\n", time.Now().Sub(startTime))
	}

	return nil
}

type options_type struct {
	width       int
	height      int
	depth       int
	srcFilename string
	dstFilename string
	filterName  string
	blur        float64
	noGamma     bool
	numThreads  int
	verbose     bool
	debug       bool
}

func main() {
	options := new(options_type)

	// Replace the standard flag.Usage function
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  fpr (-w|-h) <n> [options] <source-file> <target-file>\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "  Available filters: lanczos, lanczos2, catrom, mitchell, hermite, bspline,\n")
		fmt.Fprintf(os.Stderr, "    gaussian, mix, box, boxavg, nearest, triangle\n")
	}

	flag.IntVar(&options.height, "h", 0, "Target image height, in pixels")
	flag.IntVar(&options.width, "w", 0, "Target image width, in pixels")
	flag.IntVar(&options.depth, "depth", 8, "Preferred bit depth, in bits per sample")
	flag.StringVar(&options.filterName, "filter", "auto", "Resampling filter to use")
	flag.Float64Var(&options.blur, "blur", 1.0, "Amount to blur")
	flag.BoolVar(&options.noGamma, "nogamma", false, "Disable color correction")
	flag.IntVar(&options.numThreads, "threads", 0, "Maximum number of worker threads")
	flag.BoolVar(&options.verbose, "verbose", false, "Verbose output")
	flag.BoolVar(&options.debug, "debug", false, "Debugging output")
	flag.Parse()

	if flag.NArg() != 2 {
		flag.Usage()
		return
	}

	if options.width < 1 && options.height < 1 {
		flag.Usage()
		return
	}

	options.srcFilename = flag.Arg(0)
	options.dstFilename = flag.Arg(1)

	err := resizeMain(options)
	if err != nil {
		fmt.Printf("Error: %v\n", err.Error())
	}
}
