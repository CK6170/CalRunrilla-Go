package main

import (
	"fmt"
	"math"
	"strings"
)

type Vector struct {
	Length int
	Values []float64
}

func NewVector(length int) *Vector {
	return &Vector{Length: length, Values: make([]float64, length)}
}

func NewVectorWithValue(length int, val float64) *Vector {
	v := NewVector(length)
	for i := range v.Values {
		v.Values[i] = val
	}
	return v
}

// ...existing code...

func (v *Vector) Norm() float64 {
	sum := 0.0
	for _, val := range v.Values {
		sum += val * val
	}
	return math.Sqrt(sum)
}

func (v *Vector) Sub(other *Vector) *Vector {
	result := NewVector(v.Length)
	for i := range v.Values {
		result.Values[i] = v.Values[i] - other.Values[i]
	}
	return result
}

func (v *Vector) ToStrings(title, format string) (string, string) {
	sb := &strings.Builder{}
	sb.WriteString(matrixline + "\n")
	sb.WriteString(title + "\n")
	// Use provided format if specified, else default integer-like
	fmtStr := "%10.0f"
	if format != "" {
		fmtStr = format
	}
	for i, val := range v.Values {
		sb.WriteString(fmt.Sprintf(fmtStr+"\n", val))
		_ = i // keep index available if needed for future formatting
	}
	sb.WriteString(matrixline)
	return sb.String(), "" // Simplified
}
