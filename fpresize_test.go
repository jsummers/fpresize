// ◄◄◄ fpresize_test.go ►►►

package fpresize

import "testing"
import "image"
import "image/color"

func sampleImage1() image.Image {
	im := image.NewNRGBA(image.Rect(0, 0, 15, 15))
	for j := 0; j < 15; j++ {
		for i := 0; i < 15; i++ {
			if i == 7 && j == 7 {
				im.SetNRGBA(i, j, color.NRGBA{230, 220, 210, 230})
			} else {
				im.SetNRGBA(i, j, color.NRGBA{50, 40, 60, 150})
			}
		}
	}
	return im
}

func checkPixel(t *testing.T, name string, im image.Image, x, y int, e_r, e_g, e_b, e_a uint32) {
	c := im.At(x, y)
	a_r, a_g, a_b, a_a := c.RGBA()
	if a_r != e_r || a_g != e_g || a_b != e_b || a_a != e_a {
		t.Logf("%s: color is %v, %v, %v, %v\n", name, a_r, a_g, a_b, a_a)
		t.Logf("%s: expected %v, %v, %v, %v\n", name, e_r, e_g, e_b, e_a)
		t.Fail()
	}
}

func TestOne(t *testing.T) {
	var err error

	fp := new(FPObject)
	src := sampleImage1()
	fp.SetSourceImage(src)
	fp.SetTargetBounds(image.Rect(0, 0, 100, 99))
	dst, err := fp.Resize()
	if err != nil {
		t.Logf("%s\n", err.Error())
		t.FailNow()
	}

	checkPixel(t, "test1", dst, 50, 54, 23662, 22361, 22141, 43098)
	checkPixel(t, "test2", dst, 43, 48, 9654, 8358, 10530, 38876)
}
