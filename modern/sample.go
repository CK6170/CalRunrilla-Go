package modern

import (
	"context"
	"fmt"
	"time"

	serialpkg "github.com/CK6170/Calrunrilla-go/serial"
)

type SamplePhase string

const (
	SamplePhaseLive      SamplePhase = "live"
	SamplePhaseIgnoring  SamplePhase = "ignoring"
	SamplePhaseAveraging SamplePhase = "averaging"
	SamplePhaseFinished  SamplePhase = "finished"
)

type SampleUpdate struct {
	Phase        SamplePhase
	IgnoreDone   int
	IgnoreTarget int
	AvgDone      int
	AvgTarget    int
	// Current raw ADCs: [bar][lc]
	Current [][]int64
	// Final averages when Phase == finished: [bar][lc]
	Final [][]int64
}

// SampleADCs performs the same ignore+average behavior as the CLI calibration flow,
// but is UI-agnostic and cancellable.
//
// It returns a flattened slice sized len(bars.Bars)*bars.NLCs in bar-major order.
func SampleADCs(
	ctx context.Context,
	bars *serialpkg.Leo485,
	ignoreTarget int,
	avgTarget int,
	onUpdate func(SampleUpdate),
) ([]int64, error) {
	if bars == nil || len(bars.Bars) == 0 {
		return nil, fmt.Errorf("bars not connected")
	}
	if ignoreTarget < 0 {
		ignoreTarget = 0
	}
	if avgTarget <= 0 {
		return nil, fmt.Errorf("avgTarget must be > 0")
	}

	phase := SamplePhaseLive
	ignoreDone := 0
	avgDone := 0

	nBars := len(bars.Bars)
	nLCs := bars.NLCs

	// sums[count] for averaging
	sums := make([][]int64, nBars)
	counts := make([][]int64, nBars)
	for i := 0; i < nBars; i++ {
		sums[i] = make([]int64, nLCs)
		counts[i] = make([]int64, nLCs)
	}

	readOnce := func() [][]int64 {
		cur := make([][]int64, nBars)
		for i := 0; i < nBars; i++ {
			bruts, err := bars.GetADs(i)
			row := make([]int64, nLCs)
			if err == nil && len(bruts) > 0 {
				for lc := 0; lc < nLCs && lc < len(bruts); lc++ {
					row[lc] = int64(bruts[lc])
				}
			}
			cur[i] = row
		}
		return cur
	}

	// initial live tick
	if onUpdate != nil {
		onUpdate(SampleUpdate{
			Phase:        phase,
			IgnoreDone:   ignoreDone,
			IgnoreTarget: ignoreTarget,
			AvgDone:      avgDone,
			AvgTarget:    avgTarget,
			Current:      readOnce(),
		})
	}

	// ignore phase
	phase = SamplePhaseIgnoring
	for ignoreDone < ignoreTarget {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		cur := readOnce()
		ignoreDone++
		if onUpdate != nil {
			onUpdate(SampleUpdate{
				Phase:        phase,
				IgnoreDone:   ignoreDone,
				IgnoreTarget: ignoreTarget,
				AvgDone:      0,
				AvgTarget:    avgTarget,
				Current:      cur,
			})
		}
		time.Sleep(5 * time.Millisecond)
	}

	// averaging phase
	phase = SamplePhaseAveraging
	for avgDone < avgTarget {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		cur := readOnce()
		avgDone++
		for i := 0; i < nBars; i++ {
			for lc := 0; lc < nLCs; lc++ {
				sums[i][lc] += cur[i][lc]
				counts[i][lc]++
			}
		}
		if onUpdate != nil {
			onUpdate(SampleUpdate{
				Phase:        phase,
				IgnoreDone:   ignoreTarget,
				IgnoreTarget: ignoreTarget,
				AvgDone:      avgDone,
				AvgTarget:    avgTarget,
				Current:      cur,
			})
		}
		time.Sleep(5 * time.Millisecond)
	}

	final := make([][]int64, nBars)
	for i := 0; i < nBars; i++ {
		final[i] = make([]int64, nLCs)
		for lc := 0; lc < nLCs; lc++ {
			if counts[i][lc] > 0 {
				final[i][lc] = sums[i][lc] / counts[i][lc]
			}
		}
	}

	if onUpdate != nil {
		onUpdate(SampleUpdate{
			Phase:        SamplePhaseFinished,
			IgnoreDone:   ignoreTarget,
			IgnoreTarget: ignoreTarget,
			AvgDone:      avgTarget,
			AvgTarget:    avgTarget,
			Current:      nil,
			Final:        final,
		})
	}

	flat := make([]int64, nBars*nLCs)
	for i := 0; i < nBars; i++ {
		for lc := 0; lc < nLCs; lc++ {
			flat[i*nLCs+lc] = final[i][lc]
		}
	}
	return flat, nil
}

