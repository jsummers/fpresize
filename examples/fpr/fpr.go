// ◄◄◄ fpr.go ►►►
// Copyright © 2012 Jason Summers

// fpr is a sample program that uses the fpresize library.
// Usage: fpr -h <height> <source-file> <target.png>
package main

import "fmt"
import "os"
import "strconv"
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

func resizeMain(srcFilename, dstFilename string, dstH int) error {
	var err error
	var srcBounds image.Rectangle
	var resizedImage *fpresize.FPImage
	var srcImg image.Image
	var srcW, srcH, dstW int

	srcImg, err = readImageFromFile(srcFilename)
	if err != nil {
		return err
	}

	fp := new(fpresize.FPObject)

	// To do colorspace-unaware resizing, call the following functions:
	// fp.SetInputColorConverter(nil)
	// fp.SetOutputColorConverter(nil)

	fp.SetSourceImage(srcImg)

	// An example of how to specify the filter to use:
	// fp.SetFilter(fpresize.MakeLanczosFilter(3))

	// The filter to use can depend on the scale factor, and can be different for
	// the vertical and horizontal dimensions. For example:
	// fp.SetFilterGetter(func(isVertical bool, scaleFactor float64) *fpresize.Filter {
	//	if scaleFactor > 1.0 {
	//		return fpresize.MakeCubicFilter(1.0/3, 1.0/3)
	//	}
	//	return fpresize.MakeLanczosFilter(3)
	//})

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
	nrgbaResizedImage := resizedImage.CopyToNRGBA()

	err = writeImageToFile(nrgbaResizedImage, dstFilename)
	if err != nil {
		return err
	}

	return nil
}

func usage() {
	fmt.Printf("usage: fpr -h <height> <source-file> <target.png>\n")
}

func main() {
	var srcFilename, dstFilename string
	var dstH int

	// TODO: use the "flag" package for command line parsing.
	if len(os.Args) != 5 {
		usage()
		return
	}
	if os.Args[1] != "-h" {
		usage()
		return
	}

	dstH, _ = strconv.Atoi(os.Args[2])
	srcFilename = os.Args[3]
	dstFilename = os.Args[4]

	err := resizeMain(srcFilename, dstFilename, dstH)
	if err != nil {
		fmt.Printf("Error: %v\n", err.Error())
	}
}
