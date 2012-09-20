fpresize
========

A Go package for high-quality raster image resizing.

Copyright (C) 2012 Jason Summers

Installation
------------

To download and install, at a shell prompt or command prompt type:

    go get github.com/jsummers/fpresize

To build the example utility, type:

    go install github.com/jsummers/fpresize/examples/fpr

The `fpr` program should appear at `GOPATH/bin/fpr`, where `GOPATH`
is based on your `GOPATH` or `GOROOT` environment variables.

Other information
-----------------

Fpresize is probably usable as it is, but it is a *work in progress*,
and the API may change.

Future plans include documentation, automated testing, and performance
improvements.

Some of fpresize was copied (and translated) from my C library,
[ImageWorsener](http://entropymine.com/imageworsener/).
But fpresize has far fewer features, and is not *quite* as accurate.
