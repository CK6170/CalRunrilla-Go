package main

import (
	"math"
)

func ToIEEE754(f float32) uint32 {
	bits := math.Float32bits(f)
	return bits
}
