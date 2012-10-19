fpresize
========

A Go package for high-quality raster image resizing.


Installation
------------

To download and install, at a command prompt type:

    go get github.com/jsummers/fpresize

To build the example utility, type:

    go install github.com/jsummers/fpresize/examples/fpr

The `fpr` program should appear at `GOPATH/bin/fpr`, where `GOPATH`
is the first path in your `GOPATH` environment variable.


Documentation
-------------

The documentation may be read online at
[GoPkgDoc](http://go.pkgdoc.org/github.com/jsummers/fpresize).

Or, after installing, type:

    godoc github.com/jsummers/fpresize | more


Status
------

New features may be added in the future, but none are specifically
planned. I will try not to break API backward-compatibility without good
reason.

Changes are likely to focus on performance. Although it is very fast
in most cases, there is room for improvement.

Fpresize always processes images as full-color RGBA. (It does avoid
processing the alpha channel if the image has no transparency, though
it still stores it.) Resizing a grayscale or alpha image is thus very
inefficient, both in memory and speed. Optimized support for these
image types may be added at some point.

Its use of multithreading could be improved, by using fewer goroutines
in some cases, and/or larger work items. It's difficult to know what
parameters to use, as it depends on things like the computer's current
workload, and the quality of Go's scheduler (which is expected to
improve).


Known bugs
----------

As of at least version 1.0.3 (2012/09/21), Go's standard PNG library has
a bug that prevents it from correctly handling paletted PNG images with
partial transparency. The bug has been fixed
(<http://code.google.com/p/go/source/detail?r=f1acac08c808>; 2012/07/31),
but I do not know when the fix will appear in a Go release.


Other information
-----------------

Parts of fpresize are based on my C library,
[ImageWorsener](http://entropymine.com/imageworsener/).
But fpresize has far fewer features, and is not *quite* as accurate.


License
-------

Fpresize is distributed under an MIT-style license.

Copyright &copy; 2012 Jason Summers
<[jason1@pobox.com](mailto:jason1@pobox.com)>

<pre>
Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
</pre>
