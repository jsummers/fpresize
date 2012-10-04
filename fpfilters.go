// ◄◄◄ fpfilters.go ►►►
// Copyright © 2012 Jason Summers

// Functions for making filters

package fpresize

import "math"

// Filter represents a resampling filter.
//
// A filter can be created by a Make*Filter function, or you can
// create a custom filter.
type Filter struct {
	// Perhaps we ought to have a more-concrete Filter type in which the
	// scaleFactor param is removed, and Radius and Flags are numeric variables.
	// But I've had trouble making that work in an elegant way.

	// F is the filter function.
	F func(x float64, scaleFactor float64) float64

	// Radius The largest distance from 0 at which the filter's value is
	// nonzero (not taking blurring into account).
	Radius func(scaleFactor float64) float64

	// Flags may affect how fpresize uses the filter. This field can be (and
	// usually is) nil.
	Flags func(scaleFactor float64) uint32
}

const (
	// If FPFlagAsymmetric is set, the filter will be called with negative
	// arguments. Otherwise, your filter can safely assume the argument is
	// nonnegative.
	FilterFlagAsymmetric = 0x00000001
)

// Returns a triangle filter.
func MakeTriangleFilter() *Filter {
	f := new(Filter)
	f.F = func(x float64, scaleFactor float64) float64 {
		if x < 1.0 {
			return 1.0 - x
		}
		return 0.0
	}
	f.Radius = func(scaleFactor float64) float64 {
		return 1.0
	}
	return f
}

// Returns a gaussian filter, evaluated out to 4 standard deviations.
func MakeGaussianFilter() *Filter {
	f := new(Filter)
	f.F = func(x float64, scaleFactor float64) float64 {
		if x >= 2.0 {
			return 0.0
		}
		v := math.Exp(-2.0*x*x) * 0.79788456080286535587989;
		if x <= 1.999 {
			return v
		}
		// Slightly alter the filter to make it continuous:
		return 1000.0*(2.0-x)*v
	}
	f.Radius = func(scaleFactor float64) float64 {
		return 2.0
	}
	return f
}

// Returns a cubic filter, based on the B and C parameters as defined by
// Mitchell/Netravali. Some options are (1./3,1./3) for a Mitchell filter,
// (0,0.5) for Catmull-Rom, and (0,0) for a Hermite filter.
func MakeCubicFilter(b float64, c float64) *Filter {
	var radius float64
	if b == 0 && c == 0 {
		radius = 1.0
	} else {
		radius = 2.0
	}
	f := new(Filter)
	f.F = func(x float64, scaleFactor float64) float64 {
		if x < 1.0 {
			return ((12.0-9.0*b-6.0*c)*x*x*x +
				(-18.0+12.0*b+6.0*c)*x*x +
				(6.0 - 2.0*b)) / 6.0
		} else if x < 2.0 {
			return ((-b-6.0*c)*x*x*x +
				(6.0*b+30.0*c)*x*x +
				(-12.0*b-48.0*c)*x +
				(8.0*b + 24.0*c)) / 6.0
		}
		return 0.0
	}
	f.Radius = func(scaleFactor float64) float64 {
		return radius
	}
	return f
}

// Returns a Lanczos filter, with the given number of "lobes". The most common
// choices are 3 and 2.
func MakeLanczosFilter(lobes int) *Filter {
	radius := float64(lobes)
	f := new(Filter)
	f.F = func(x float64, scaleFactor float64) float64 {
		if x < radius {
			return Sinc(x) * Sinc(x/radius)
		}
		return 0.0
	}
	f.Radius = func(scaleFactor float64) float64 {
		return radius
	}
	return f
}

// Returns a filter that performs pixel mixing, also known as pixel averaging or
// area map.
func MakePixelMixingFilter() *Filter {
	f := new(Filter)
	f.F = func(x float64, scaleFactor float64) float64 {
		var p float64
		if scaleFactor < 1.0 {
			p = scaleFactor
		} else {
			p = 1.0 / scaleFactor
		}
		if x < 0.5-p/2.0 {
			return 1.0
		} else if x < 0.5+p/2.0 {
			return 0.5 - (x-0.5)/p
		}
		return 0.0
	}
	f.Radius = func(scaleFactor float64) float64 {
		if scaleFactor < 1.0 {
			return 0.5 + scaleFactor
		}
		return 0.5 + 1.0/scaleFactor
	}
	return f
}

// Sinc is the mathematical sinc function, sin(pi*x)/(pi*x).
// It's public because it may be useful in custom filters.
func Sinc(x float64) float64 {
	if x <= 0.000000005 && x >= -0.000000005 {
		return 1.0
	}
	return math.Sin(math.Pi*x) / (math.Pi * x)
}
