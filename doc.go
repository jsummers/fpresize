// ◄◄◄ doc.go ►►►
// Copyright © 2012 Jason Summers

/*
Package fpresize performs high-quality resizing of raster images.

This is a brief summary of how to use the package. More details are
available in the API documentation later in this document.

Fpresize resizes image objects that satisfy the image.Image interface
from Go's "image" package. You can create such an object using the
image.Decode method to read from a file, or from scratch with a method such
as image.NewRGBA. The steps below assume you have a pointer to such an
image in a variable named sourceImage.

Import the fpresize package, and any other packages you need:

    import "image"
    import "runtime"
    import "github.com/jsummers/fpresize"

For best performance, make sure your program allows multiple CPUs to be used:

    runtime.GOMAXPROCS(runtime.NumCPU())

Create a new FPObject based on your source image:

    fp := fpresize.New(sourceImage)

Tell fpresize what size, in pixels, to make the new image:

    fp.SetTargetBounds(image.Rect(0, 0, 500, 500))

At this point, there are several optional methods you can call to control how
the image is resized. Notably, you can change the resampling filter with
SetFilter(), or disable color correction by calling
SetInputColorConverter(nil); SetOutputColorConverter(nil).

Now resize the image, by calling one of the available Resize* methods:
ResizeToRGBA, ResizeToNRGBA, ResizeToRGBA64, ResizeToNRGBA64, or Resize.

    resizedImage, err := fp.ResizeToRGBA()

Read the documentation for these methods for details about which one to use.
Unfortunately, a one-size-fits-all method would make certain optimizations
impossible. If fpresize knows what sort of resized image you want, it can
usually take advantage of that to run faster and use less memory.

You can write the resized image to a file by using the Encode method from
image/jpeg, image/png, or another image package.
*/
package fpresize
