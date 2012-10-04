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
is the first path in your `GOPATH`  environment variables.


Documentation
-------------

Documentation is incomplete. After installing, type:

    godoc github.com/jsummers/fpresize | more


Other information
-----------------

Fpresize is almost feature-complete. I will try not to break
backward-compatibility without a really good reason.

Future plans include better documentation, automated testing, and
performance improvements.

Some of fpresize was copied (and translated) from my C library,
[ImageWorsener](http://entropymine.com/imageworsener/).
But fpresize has far fewer features, and is not *quite* as accurate.

Copyright &copy; 2012 Jason Summers
<[jason1@pobox.com](mailto:jason1@pobox.com)>
