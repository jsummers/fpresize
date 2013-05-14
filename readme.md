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
[GoDoc](http://godoc.org/github.com/jsummers/fpresize).

Or, after installing, type:

    godoc github.com/jsummers/fpresize | more


Status
------

New features may be added in the future, but none are specifically
planned. I will try not to break API backward-compatibility without good
reason.

Fpresize always stores images as full-color RGBA. It has optimizations to
avoid some of the unnecessary work if the image is fully opaque, or is
grayscale, but it still uses the extra memory.

Fpresize is generally very fast when processing images with a depth of 8 bits
per sample. It can be much slower when the bit depth is higher, in part
because less effort has been put into optimizing the code for such images.

Its use of multithreading could be improved, by using fewer goroutines
in some cases, and/or larger work items. It's difficult to know what
parameters to use, as it depends on things like the computer's current
workload, and the quality of Go's scheduler (which is expected to
improve).


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
