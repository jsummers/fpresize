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
var lastMsgTime time.Time

func progressMsg(options *options_type, msg string) {
	if !options.verbose && !options.debug {
		return
	}
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

func resizeMain(options *options_type) error {
	var err error
	var srcBounds image.Rectangle
	var rgbaResizedImage *image.RGBA
	var nrgbaResizedImage *image.NRGBA
	var srcImg image.Image
	var srcW, srcH, dstW, dstH int
	var outputFileFormat int

	startTime := time.Now()

	// Allow more than one thread to be used by this process, if more than one CPU exists.
	runtime.GOMAXPROCS(runtime.NumCPU())

	dstH = options.height

	outputFileFormat = getFileFormatByFilename(options.dstFilename)
	if outputFileFormat == ffUnknown {
		return errors.New("Can't determine output file format. Please name the output file to end in .png or .jpg")
	}

	progressMsg(options, "Reading source file")
	srcImg, err = readImageFromFile(options.srcFilename)
	if err != nil {
		return err
	}

	fp := fpresize.New(srcImg)

	fp.SetProgressCallback(func(msg string) { progressMsg(options, msg) })

	if options.noGamma {
		// To do colorspace-unaware resizing, call the following methods:
		fp.SetInputColorConverter(nil)
		fp.SetOutputColorConverter(nil)
	}

	if options.filterName == "auto" {
	} else if options.filterName == "lanczos2" {
		fp.SetFilter(fpresize.MakeLanczosFilter(2))
	} else if options.filterName == "lanczos3" || options.filterName == "lanczos" {
		fp.SetFilter(fpresize.MakeLanczosFilter(3))
	} else if options.filterName == "mix" {
		fp.SetFilter(fpresize.MakePixelMixingFilter())
	} else if options.filterName == "mitchell" {
		fp.SetFilter(fpresize.MakeCubicFilter(1.0/3.0, 1.0/3.0))
	} else if options.filterName == "catrom" {
		fp.SetFilter(fpresize.MakeCubicFilter(0.0, 0.5))
	} else if options.filterName == "hermite" {
		fp.SetFilter(fpresize.MakeCubicFilter(0.0, 0.0))
	} else if options.filterName == "gaussian" {
		fp.SetFilter(fpresize.MakeGaussianFilter())
	} else if options.filterName == "triangle" {
		fp.SetFilter(fpresize.MakeTriangleFilter())
	} else if options.filterName == "boxavg" {
		fp.SetFilter(fpresize.MakeBoxAvgFilter())
	} else {
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

	// Decide on the width of the resized image.
	srcBounds = srcImg.Bounds()
	srcW = srcBounds.Max.X - srcBounds.Min.X
	srcH = srcBounds.Max.Y - srcBounds.Min.Y
	dstW = int(0.5 + (float64(srcW)/float64(srcH))*float64(dstH))
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}
	fp.SetTargetBounds(image.Rect(0, 0, dstW, dstH))

	// Do the resize.
	if outputFileFormat == ffPNG {
		nrgbaResizedImage, err = fp.ResizeToNRGBA()
	} else {
		rgbaResizedImage, err = fp.ResizeToRGBA()
	}
	if err != nil {
		return err
	}

	progressMsg(options, "Writing target file")
	if nrgbaResizedImage != nil {
		err = writeImageToFile(nrgbaResizedImage, options.dstFilename, outputFileFormat)
	} else {
		err = writeImageToFile(rgbaResizedImage, options.dstFilename, outputFileFormat)
	}
	if err != nil {
		return err
	}

	if options.debug {
		progressMsg(options, "Done")
		fmt.Printf("Total time: %v\n", time.Now().Sub(startTime))
	} else {
		progressMsg(options, "Done")
	}

	return nil
}

type options_type struct {
	height      int
	srcFilename string
	dstFilename string
	filterName  string
	blur        float64
	noGamma     bool
	verbose     bool
	debug       bool
}

func main() {
	options := new(options_type)

	// Replace the standard flag.Usage function
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  fpr -h <height> [options] <source-file> <target-file>\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "  Available filters: lanczos, lanczos2, catrom, mitchell, hermite, gaussian,\n");
		fmt.Fprintf(os.Stderr, "    mix, boxavg, triangle\n")
	}

	flag.IntVar(&options.height, "h", 0, "Target image height, in pixels")
	flag.StringVar(&options.filterName, "filter", "auto", "Resampling filter to use")
	flag.Float64Var(&options.blur, "blur", 1.0, "Amount to blur")
	flag.BoolVar(&options.noGamma, "nogamma", false, "Disable color correction")
	flag.BoolVar(&options.verbose, "verbose", false, "Verbose output")
	flag.BoolVar(&options.debug, "debug", false, "Debugging output")
	flag.Parse()

	if flag.NArg() != 2 {
		flag.Usage()
		return
	}

	if options.height < 1 {
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
