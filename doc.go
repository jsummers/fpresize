// ◄◄◄ doc.go ►►►
// Copyright © 2012 Jason Summers

/*
Fpresize is a library that performs high-quality resizing of raster images.

Fpresize resizes image objects that satisfy the image.Image interface
from Go's "image" package. You can create such an object
using the image.Decode method to read from a file, or with a method such
as image.NewRGBA. We'll assume you have a pointer to such an image in
a variable named sourceImage.

Import packages:

    import "image"
    import "runtime"
    import "github.com/jsummers/fpresize"

For best performance, make sure your program allows multiple CPUs to be used:

    runtime.GOMAXPROCS(runtime.NumCPU())

Create a new FPObject:

    fp := new(fpresize.FPObject)

Give fpresize a pointer to your source image:

    fp.SetSourceImage(sourceImage)

Tell fpresize what size you want the new image to be, in pixels:

    fp.SetTargetBounds(image.Rect(0, 0, 500, 500))

Resize the image, by calling one of the available Resize* methods:
ResizeToRGBA, ResizeToNRGBA, ResizeToRGBA64, ResizeToNRGBA64, or Resize.

    resizedImage, err := fp.ResizeToRGBA()

Read the full API documentation below for details about which method to use.
Unfortunately, a one-size-fits-all method would make certain optimizations
impossible. If fpresize knows what sort of resized image you want, it can
usually take advantage of that to run faster and use less memory.

You can write the resized image to a file by using the Encode method from
image.jpeg, image.png, or another image package.

You can call multiple Resize methods to resize the same source image in
different ways. This is faster than starting over and reading the source image
again. To resize a different source image, you must create a new FPObject.

Before calling a Resize method, there are several options you can change to
affect how the image is resized. Notably, you can change the resampling
filter with SetFilter, or disable color correction (and improve speed) by
calling SetInputColorConverter(nil); SetOutputColorConverter(nil).
*/
package fpresize
