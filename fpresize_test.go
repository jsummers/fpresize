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
import "image/png"

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

func runFileTest(t *testing.T, opts *testOptions) {
	var src image.Image
	var dst image.Image
	var err error

	fp := new(FPObject)
	src = readImageFromFile(t, opts.srcImgDir+opts.infn)

	fp.SetSourceImage(src)
	if opts.advancedBounds {
		fp.SetTargetBoundsAdvanced(opts.bounds, opts.adv_x1, opts.adv_y1, opts.adv_x2, opts.adv_y2)
	} else {
		fp.SetTargetBounds(opts.bounds)
	}

	if opts.filter != nil {
		fp.SetFilter(opts.filter)
	}

	dst, err = fp.ResizeToNRGBA()
	if err != nil {
		t.Logf("%s\n", err.Error())
		t.FailNow()
	}

	writeImageToFile(t, dst, opts.actualDir+opts.outfn)

	// Comparing output files is not ideal (comparing pixel colors would be
	// better), but it will do.
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

	advancedBounds bool
	adv_x1, adv_y1 float64
	adv_x2, adv_y2 float64
}

func resetOpts(opts *testOptions) {
	opts.infn = ""
	opts.outfn = ""
	opts.bounds.Min.X = 0
	opts.bounds.Min.Y = 0
	opts.bounds.Max.X = 19
	opts.bounds.Max.Y = 19
	opts.filter = nil
	opts.advancedBounds = false
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
	opts.bounds.Max.X = 21
	opts.bounds.Max.Y = 22
	opts.advancedBounds = true
	opts.adv_x1 = 0.5
	opts.adv_y1 = 2.0
	opts.adv_x2 = 20.5
	opts.adv_y2 = 21.0
	opts.filter = MakeLanczosFilter(4)
	runFileTest(t, opts)
}
