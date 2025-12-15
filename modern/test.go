package modern

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/CK6170/Calrunrilla-go/matrix"
	"github.com/CK6170/Calrunrilla-go/models"
	serialpkg "github.com/CK6170/Calrunrilla-go/serial"
)

// EnsureFactorsFromDevice populates p.BARS[i].LC[].FACTOR if the config file is not a calibrated json.
func EnsureFactorsFromDevice(ctx context.Context, bars *serialpkg.Leo485, p *models.PARAMETERS, configPath string) error {
	if bars == nil {
		return fmt.Errorf("bars not connected")
	}
	if p == nil {
		return fmt.Errorf("parameters nil")
	}
	if strings.HasSuffix(strings.ToLower(configPath), "_calibrated.json") {
		return nil
	}
	for i := 0; i < len(bars.Bars); i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		factors, err := bars.ReadFactors(i)
		if err != nil || len(factors) == 0 {
			return fmt.Errorf("bar %d: could not read factors: %v", i+1, err)
		}
		nlcs := len(factors)
		p.BARS[i].LC = make([]*models.LC, nlcs)
		for j := 0; j < nlcs; j++ {
			f := float32(factors[j])
			p.BARS[i].LC[j] = &models.LC{
				ZERO:   0,
				FACTOR: f,
				IEEE:   fmt.Sprintf("%08X", matrix.ToIEEE754(f)),
			}
		}
	}
	return nil
}

type ZeroProgress struct {
	WarmupDone   int
	WarmupTarget int
	SampleDone   int
	SampleTarget int
}

// CollectAveragedZeros returns flattened zeros (bar-major order) similar to CLI test mode.
func CollectAveragedZeros(ctx context.Context, bars *serialpkg.Leo485, p *models.PARAMETERS, samples int, onProgress func(ZeroProgress)) ([]int64, error) {
	if bars == nil {
		return nil, fmt.Errorf("bars not connected")
	}
	if samples <= 0 {
		return nil, fmt.Errorf("samples must be > 0")
	}
	nb := len(bars.Bars)
	nlcs := bars.NLCs
	sums := make([]int64, nb*nlcs)
	count := 0
	warmup := 5
	if p != nil && p.IGNORE > 0 {
		warmup = p.IGNORE
	}

	emit := func(z ZeroProgress) {
		if onProgress != nil {
			onProgress(z)
		}
	}

	for w := 0; w < warmup; w++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		for i := 0; i < nb; i++ {
			_, _ = bars.GetADs(i)
		}
		emit(ZeroProgress{WarmupDone: w + 1, WarmupTarget: warmup, SampleDone: 0, SampleTarget: samples})
		time.Sleep(5 * time.Millisecond)
	}

	for s := 0; s < samples; s++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		gotAny := false
		for i := 0; i < nb; i++ {
			ad, err := bars.GetADs(i)
			if err != nil || len(ad) == 0 {
				continue
			}
			gotAny = true
			for lc := 0; lc < nlcs; lc++ {
				val := int64(0)
				if lc < len(ad) {
					val = int64(ad[lc])
				}
				idx := i*nlcs + lc
				sums[idx] += val
			}
		}
		if gotAny {
			count++
		}
		emit(ZeroProgress{WarmupDone: warmup, WarmupTarget: warmup, SampleDone: s + 1, SampleTarget: samples})
		time.Sleep(5 * time.Millisecond)
	}

	avg := make([]int64, nb*nlcs)
	if count == 0 {
		// one-shot fallback
		for i := 0; i < nb; i++ {
			ad, err := bars.GetADs(i)
			if err != nil || len(ad) == 0 {
				continue
			}
			for lc := 0; lc < nlcs; lc++ {
				idx := i*nlcs + lc
				if lc < len(ad) {
					avg[idx] = int64(ad[lc])
				}
			}
		}
		return avg, nil
	}

	for i := range sums {
		avg[i] = sums[i] / int64(count)
	}
	return avg, nil
}

type TestSnapshot struct {
	PerBarLCWeight [][]float64
	PerBarTotal    []float64
	GrandTotal     float64
	PerBarADC      [][]int64
}

func ComputeTestSnapshot(bars *serialpkg.Leo485, p *models.PARAMETERS, zerosFlat []int64) (*TestSnapshot, error) {
	if bars == nil {
		return nil, fmt.Errorf("bars not connected")
	}
	if p == nil {
		return nil, fmt.Errorf("parameters nil")
	}
	nb := len(p.BARS)
	nlcs := bars.NLCs
	zerosPerBar := make([][]int64, nb)
	for i := 0; i < nb; i++ {
		zerosPerBar[i] = make([]int64, nlcs)
		for j := 0; j < nlcs; j++ {
			idx := i*nlcs + j
			if idx < len(zerosFlat) {
				zerosPerBar[i][j] = zerosFlat[idx]
			}
		}
	}

	out := &TestSnapshot{
		PerBarLCWeight: make([][]float64, nb),
		PerBarTotal:    make([]float64, nb),
		PerBarADC:      make([][]int64, nb),
	}
	for i := 0; i < nb; i++ {
		ad, err := bars.GetADs(i)
		if err != nil {
			return nil, fmt.Errorf("bar %d read error: %w", i+1, err)
		}
		out.PerBarLCWeight[i] = make([]float64, nlcs)
		out.PerBarADC[i] = make([]int64, nlcs)
		total := 0.0
		for lc := 0; lc < nlcs; lc++ {
			adc := int64(0)
			if lc < len(ad) {
				adc = int64(ad[lc])
			}
			out.PerBarADC[i][lc] = adc
			zero := float64(zerosPerBar[i][lc])
			factor := float64(1)
			if lc < len(p.BARS[i].LC) {
				factor = float64(p.BARS[i].LC[lc].FACTOR)
			}
			w := (float64(adc) - zero) * factor
			out.PerBarLCWeight[i][lc] = w
			total += w
		}
		out.PerBarTotal[i] = total
		out.GrandTotal += total
	}
	return out, nil
}

