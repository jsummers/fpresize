// ◄◄◄ fpresize_test.go ►►►

// This file implements regression tests for fpresize. It is not a part of
// the main fpresize library.

package fpresize

import "testing"
import "fmt"
import "os"
import "bytes"
import "io/ioutil"
import "runtime"
import "image"
import "image/draw"
import "image/png"
import _ "image/jpeg"

func readImageFromFile(t *testing.T, srcFilename string) image.Image {
	var err error
	var srcImg image.Image

	file, err := os.Open(srcFilename)
	if err != nil {
		t.Logf("%s\n", err.Error())
		t.FailNow()
		return nil
	}
	defer file.Close()

	srcImg, _, err = image.Decode(file)
	if err != nil {
		t.Logf("%s\n", err.Error())
		t.FailNow()
		return nil
	}

	return srcImg
}

func writeImageToFile(t *testing.T, img image.Image, dstFilename string) {
	var err error

	file, err := os.Create(dstFilename)
	if err != nil {
		t.Logf("%s\n", err.Error())
		t.FailNow()
		return
	}
	defer file.Close()

	err = png.Encode(file, img)
	if err != nil {
		t.Logf("%s\n", err.Error())
		t.FailNow()
		return
	}
}

func compareFiles(t *testing.T, expectedFN string, actualFN string) {
	var expectedBytes []byte
	var actualBytes []byte
	var err error

	expectedBytes, err = ioutil.ReadFile(expectedFN)
	if err != nil {
		t.Logf("Failed to open for compare: %s\n", err.Error())
		t.Fail()
		return
	}

	actualBytes, err = ioutil.ReadFile(actualFN)
	if err != nil {
		t.Logf("Failed to open for compare: %s\n", err.Error())
		t.FailNow()
		return
	}

	if len(expectedBytes) != len(actualBytes) {
		t.Logf("%s and %s differ in size\n", expectedFN, actualFN)
		t.Fail()
		return
	}

	if 0 != bytes.Compare(actualBytes, expectedBytes) {
		t.Logf("%s and %s differ\n", expectedFN, actualFN)
		t.Fail()
		return
	}
}

// We need to test RGBA source images with transparency, because we
// have an optimized code path for that. But there's no obvious way to make
// image.Decode() create such an image, so we use this function to convert
// an image to RGBA format.
func convertToRGBA(t *testing.T, src image.Image) *image.RGBA {
	var dst *image.RGBA
	var bounds image.Rectangle

	bounds = src.Bounds()
	dst = image.NewRGBA(bounds)
	draw.Draw(dst, bounds, src, image.ZP, draw.Src)
	return dst
}

func runFileTest(t *testing.T, opts *testOptions) {
	var src image.Image
	var dst image.Image
	var dstBounds image.Rectangle
	var err error

	src = readImageFromFile(t, opts.srcImgDir+opts.infn)

	if opts.convertToRGBA {
		src = convertToRGBA(t, src)
	}

	fp := New(src)

	if opts.advancedBounds {
		fp.SetTargetBoundsAdvanced(opts.bounds, opts.adv_x1, opts.adv_y1, opts.adv_x2, opts.adv_y2)
	} else {
		fp.SetTargetBounds(opts.bounds)
	}

	if opts.filter != nil {
		fp.SetFilter(opts.filter)
	}
	if opts.blur > 0.0 {
		fp.SetBlur(opts.blur)
	}

	if opts.disableInputGamma {
		fp.SetInputColorConverter(nil)
	}
	if opts.disableOutputGamma {
		fp.SetOutputColorConverter(nil)
	}

	switch opts.outFmt {
	case outFmtFP:
		dst, err = fp.Resize()
	case outFmtRGBA:
		dst, err = fp.ResizeToRGBA()
	case outFmtRGBA64:
		dst, err = fp.ResizeToRGBA64()
	case outFmtNRGBA64:
		dst, err = fp.ResizeToNRGBA64()
	default:
		dst, err = fp.ResizeToNRGBA()
	}
	if err != nil {
		t.Logf("%s\n", err.Error())
		t.FailNow()
	}

	dstBounds = dst.Bounds()
	if dstBounds != opts.bounds {
		t.Logf("%s: incorrect bounds %v, expected %v", opts.outfn, dstBounds, opts.bounds)
		t.Fail()
	}

	writeImageToFile(t, dst, opts.actualDir+opts.outfn)
	compareFiles(t, opts.expectedDir+opts.outfn, opts.actualDir+opts.outfn)
}

func runDrawTest(t *testing.T, opts *testOptions) {
	var src image.Image
	var dst1 draw.Image
	var dst2 image.Image
	var err error

	src = readImageFromFile(t, opts.srcImgDir+opts.infn)
	fp := New(src)
	fp.SetTargetBounds(image.Rect(0, 0, 28, 28))
	dst1, err = fp.Resize()
	if err != nil {
		t.Logf("%s\n", err.Error())
		t.FailNow()
	}

	fp.SetTargetBounds(image.Rect(0, 0, 20, 15))
	if opts.trnsTest1 {
		fp.SetVirtualPixels(VirtualPixelsTransparent)
	}
	dst2, err = fp.Resize()
	if err != nil {
		t.Logf("%s\n", err.Error())
		t.FailNow()
	}

	if opts.trnsTest1 {
		if !fp.HasTransparency() {
			t.Logf("%s: HasTransparency()==false, should be true\n", opts.outfn)
			t.Fail()
		}
	}

	// Draw dst2 onto dst1
	draw.DrawMask(dst1, image.Rect(2, 11, 22, 26), dst2, image.ZP,
		dst2, image.ZP, draw.Over)
	writeImageToFile(t, dst1, opts.actualDir+opts.outfn)
	compareFiles(t, opts.expectedDir+opts.outfn, opts.actualDir+opts.outfn)
}

type testOptions struct {
	srcImgDir   string
	actualDir   string
	expectedDir string
	infn        string
	outfn       string
	bounds      image.Rectangle
	filter      *Filter
	blur        float64

	advancedBounds bool
	adv_x1, adv_y1 float64
	adv_x2, adv_y2 float64
	outFmt         int

	disableInputGamma  bool
	disableOutputGamma bool
	convertToRGBA      bool
	trnsTest1          bool
}

const (
	outFmtFP      = iota
	outFmtRGBA    = iota
	outFmtNRGBA   = iota
	outFmtRGBA64  = iota
	outFmtNRGBA64 = iota
)

const (
	ffPNG  = 1
	ffJPEG = 2
)

func resetOpts(opts *testOptions) {
	opts.infn = ""
	opts.outfn = ""
	opts.bounds.Min.X = 0
	opts.bounds.Min.Y = 0
	opts.bounds.Max.X = 19
	opts.bounds.Max.Y = 19
	opts.filter = nil
	opts.blur = -1.0
	opts.advancedBounds = false
	opts.outFmt = outFmtNRGBA
	opts.disableInputGamma = false
	opts.disableOutputGamma = false
	opts.convertToRGBA = false
}

func TestMain(t *testing.T) {
	runtime.GOMAXPROCS(3)

	opts := new(testOptions)

	// These tests assume that "go test" sets the current directory to the projects'
	// main source directory.
	opts.srcImgDir = fmt.Sprintf("testdata%csrcimg%c", os.PathSeparator, os.PathSeparator)
	opts.actualDir = fmt.Sprintf("testdata%cactual%c", os.PathSeparator, os.PathSeparator)
	opts.expectedDir = fmt.Sprintf("testdata%cexpected%c", os.PathSeparator, os.PathSeparator)

	resetOpts(opts)
	opts.outfn = "test1.png"
	opts.infn = "rgb8a.png"
	opts.bounds.Max.X = 20
	opts.bounds.Max.Y = 18
	runFileTest(t, opts)

	resetOpts(opts)
	opts.outfn = "test2.png"
	opts.infn = "rgb8.png"
	opts.bounds.Max.X = 29
	opts.bounds.Max.Y = 28
	runFileTest(t, opts)

	resetOpts(opts)
	opts.outfn = "test3.png"
	opts.infn = "rgb16a.png"
	opts.bounds.Max.X = 17
	opts.bounds.Max.Y = 17
	runFileTest(t, opts)

	resetOpts(opts)
	opts.outfn = "test4.png"
	opts.infn = "rgb8.png"
	opts.bounds.Min.X = 100
	opts.bounds.Min.Y = 100
	opts.bounds.Max.X = 121
	opts.bounds.Max.Y = 122
	opts.advancedBounds = true
	opts.adv_x1 = 100.5
	opts.adv_y1 = 102.0
	opts.adv_x2 = 120.5
	opts.adv_y2 = 121.0
	opts.filter = MakeLanczosFilter(4)
	runFileTest(t, opts)

	resetOpts(opts)
	opts.outfn = "test5.png"
	opts.infn = "rgb16a.png"
	opts.bounds.Max.X = 18
	opts.bounds.Max.Y = 18
	opts.filter = MakeCubicFilter(1.0/3.0, 1.0/3.0)
	opts.outFmt = outFmtFP
	runFileTest(t, opts)

	resetOpts(opts)
	opts.outfn = "test6.png"
	opts.infn = "rgb16a.png"
	opts.bounds.Max.X = 18
	opts.bounds.Max.Y = 18
	opts.filter = MakeTriangleFilter()
	opts.outFmt = outFmtRGBA
	runFileTest(t, opts)

	resetOpts(opts)
	opts.outfn = "test7.png"
	opts.infn = "rgb8a.png"
	opts.bounds.Max.X = 18
	opts.bounds.Max.Y = 18
	opts.filter = MakePixelMixingFilter()
	opts.outFmt = outFmtRGBA64
	runFileTest(t, opts)

	resetOpts(opts)
	opts.outfn = "test8.png"
	opts.infn = "rgb16a.png"
	opts.bounds.Max.X = 18
	opts.bounds.Max.Y = 18
	opts.filter = MakeGaussianFilter()
	opts.outFmt = outFmtNRGBA64
	runFileTest(t, opts)

	// --- Tests without gamma correction
	resetOpts(opts)
	opts.outfn = "test9.png"
	opts.infn = "rgb16a.png"
	opts.bounds.Max.X = 17
	opts.bounds.Max.Y = 17
	opts.filter = MakeCubicFilter(0.0, 0.5)
	opts.disableInputGamma = true
	opts.disableOutputGamma = true
	opts.outFmt = outFmtFP
	runFileTest(t, opts)

	resetOpts(opts)
	opts.outfn = "test10.png"
	opts.infn = "rgb16a.png"
	opts.bounds.Max.X = 17
	opts.bounds.Max.Y = 17
	opts.filter = MakeCubicFilter(0.0, 1.0)
	opts.disableInputGamma = true
	opts.disableOutputGamma = true
	opts.outFmt = outFmtRGBA
	runFileTest(t, opts)

	resetOpts(opts)
	opts.outfn = "test11.png"
	opts.infn = "rgb16a.png"
	opts.bounds.Max.X = 17
	opts.bounds.Max.Y = 17
	opts.filter = MakeCubicFilter(0.0, 0.0)
	opts.disableInputGamma = true
	opts.disableOutputGamma = true
	opts.outFmt = outFmtNRGBA
	runFileTest(t, opts)

	resetOpts(opts)
	opts.outfn = "test12.png"
	opts.infn = "rgb16a.png"
	opts.bounds.Max.X = 17
	opts.bounds.Max.Y = 17
	opts.filter = MakeLanczosFilter(3)
	opts.blur = 1.7
	opts.disableInputGamma = true
	opts.disableOutputGamma = true
	opts.outFmt = outFmtRGBA64
	runFileTest(t, opts)

	resetOpts(opts)
	opts.outfn = "test13.png"
	opts.infn = "rgb16a.png"
	opts.bounds.Max.X = 17
	opts.bounds.Max.Y = 17
	opts.filter = MakeBoxAvgFilter()
	opts.blur = 2.5
	opts.disableInputGamma = true
	opts.disableOutputGamma = true
	opts.outFmt = outFmtNRGBA64
	runFileTest(t, opts)
	// ---

	resetOpts(opts)
	opts.outfn = "test14.png"
	opts.infn = "rgb8-22.jpg"
	opts.bounds.Min.X = 10
	opts.bounds.Min.Y = 11
	opts.bounds.Max.X = 31
	opts.bounds.Max.Y = 33
	runFileTest(t, opts)

	resetOpts(opts)
	opts.outfn = "test15.png"
	opts.infn = "rgb8a.png"
	opts.bounds.Max.X = 17
	opts.bounds.Max.Y = 18
	opts.convertToRGBA = true
	runFileTest(t, opts)

	resetOpts(opts)
	opts.infn = "rgb8a.png"
	opts.outfn = "test16.png"
	runDrawTest(t, opts)

	resetOpts(opts)
	opts.outfn = "test17.png"
	opts.infn = "rgb8-11.jpg"
	opts.bounds.Max.X = 21
	opts.bounds.Max.Y = 22
	opts.advancedBounds = true
	opts.adv_x1 = 2.2
	opts.adv_y1 = 2.2
	opts.adv_x2 = 21.0 + 2.2
	opts.adv_y2 = 22.0 + 2.2
	runFileTest(t, opts)

	resetOpts(opts)
	opts.infn = "rgb8.png"
	opts.outfn = "test18.png"
	opts.trnsTest1 = true
	runDrawTest(t, opts)

	resetOpts(opts)
	opts.outfn = "test19.png"
	opts.infn = "g8.png"
	opts.bounds.Max.X = 18
	opts.bounds.Max.Y = 18
	runFileTest(t, opts)

	resetOpts(opts)
	opts.outfn = "test20.png"
	opts.infn = "g16.png"
	opts.bounds.Max.X = 18
	opts.bounds.Max.Y = 18
	runFileTest(t, opts)
}
