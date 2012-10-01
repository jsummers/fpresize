// ◄◄◄ fpr.go ►►►
// Copyright © 2012 Jason Summers

// fpr is a sample program that uses the fpresize library.
// Usage: fpr -h <height> <source-file> <target.png>
package main

import "fmt"
import "os"
import "time"
import "flag"
import "runtime"
import "image"
import "image/png"
import _ "image/jpeg"
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

func writeImageToFile(img image.Image, dstFilename string) error {
	var err error

	file, err := os.Create(dstFilename)
	if err != nil {
		return err
	}
	defer file.Close()

	err = png.Encode(file, img)
	return err
}

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

func resizeMain(options *options_type) error {
	var err error
	var srcBounds image.Rectangle
	var resizedImage *fpresize.FPImage
	var srcImg image.Image
	var srcW, srcH, dstW, dstH int

	// Allow more than one thread to be used by this process, if more than one CPU exists.
	runtime.GOMAXPROCS(runtime.NumCPU())

	dstH = options.height

	progressMsg(options, "Reading source file")
	srcImg, err = readImageFromFile(options.srcFilename)
	if err != nil {
		return err
	}

	fp := new(fpresize.FPObject)

	fp.SetProgressCallback(func(msg string) { progressMsg(options, msg) })

	// To do colorspace-unaware resizing, call the following functions:
	// fp.SetInputColorConverter(nil)
	// fp.SetOutputColorConverter(nil)

	fp.SetSourceImage(srcImg)

	// An example of how to specify the filter to use:
	// fp.SetFilter(fpresize.MakeLanczosFilter(3))

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
	// fp.SetBlur(3.0)

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
	resizedImage, err = fp.Resize()
	if err != nil {
		return err
	}

	// It's okay to pass resizedImage directly to png.Encode (etc.), but there
	// are some reasons to convert it to an NRGBA image first:
	// - The current version of the image.png package usually writes PNG files
	//   with a sample depth of 16 bits, so the files are very large. But if you
	//   pass it an NRGBA image, it uses 8 bits, which was probably what you
	//   really wanted.
	// - It's very slightly more accurate, because we may avoid converting to
	//   associated alpha and back to unassociated alpha.
	// - It's probably faster, because CopyToNRGBA knows about resizedImage's
	//   internal format, while png.Encode has to use the public accessor
	//   methods.
	progressMsg(options, "Converting to NRGBA format")
	nrgbaResizedImage := resizedImage.CopyToNRGBA()

	progressMsg(options, "Writing target file")
	err = writeImageToFile(nrgbaResizedImage, options.dstFilename)
	if err != nil {
		return err
	}
	progressMsg(options, "Done")

	return nil
}

type options_type struct {
	height      int
	srcFilename string
	dstFilename string
	verbose     bool
	debug       bool
}

func main() {
	options := new(options_type)

	// Replace the standard flag.Usage function
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  fpr [options] <source-file> <target.png>\n")
		flag.PrintDefaults()
	}

	flag.IntVar(&options.height, "h", 0, "Target image height, in pixels")
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
