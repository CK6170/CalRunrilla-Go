package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/CK6170/Calrunrilla-go/matrix"
	"github.com/CK6170/Calrunrilla-go/models"
	serialpkg "github.com/CK6170/Calrunrilla-go/serial"
)

type CalStepKind string

const (
	CalStepZero   CalStepKind = "zero"
	CalStepWeight CalStepKind = "weight"
)

type CalStep struct {
	Kind   CalStepKind
	Index  int
	Label  string
	Prompt string
}

func buildCalibrationPlan(p *models.PARAMETERS, nlcs int) ([]CalStep, int, error) {
	if p == nil {
		return nil, 0, fmt.Errorf("parameters nil")
	}
	if len(p.BARS) == 0 {
		return nil, 0, fmt.Errorf("no bars configured")
	}
	if nlcs <= 0 {
		return nil, 0, fmt.Errorf("nlcs must be > 0")
	}
	nbars := len(p.BARS)
	nloads := 3 * (nbars - 1) * nlcs
	steps := make([]CalStep, 0, 1+nloads)
	steps = append(steps, CalStep{
		Kind:   CalStepZero,
		Index:  -1,
		Label:  "[ZERO]",
		Prompt: "Clear the Bay(s) and start sampling.",
	})
	for j := 0; j < nloads; j++ {
		msg := fmt.Sprintf(
			"Put %d on the %s Bay on the %s side in the %s of the Shelf.",
			p.WEIGHT,
			models.BAY(j/6),
			models.LMR((j/2)%3),
			models.FB(j%2),
		)
		steps = append(steps, CalStep{
			Kind:   CalStepWeight,
			Index:  j,
			Label:  fmt.Sprintf("[%04d]", j+1),
			Prompt: msg,
		})
	}
	return steps, nloads, nil
}

func sampleADCs(ctx context.Context, bars *serialpkg.Leo485, ignoreTarget, avgTarget int, onUpdate func(map[string]interface{})) ([]int64, error) {
	if bars == nil || len(bars.Bars) == 0 {
		return nil, fmt.Errorf("bars not connected")
	}
	if ignoreTarget < 0 {
		ignoreTarget = 0
	}
	if avgTarget <= 0 {
		return nil, fmt.Errorf("avgTarget must be > 0")
	}

	nBars := len(bars.Bars)
	nLCs := bars.NLCs

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

	emit := func(m map[string]interface{}) {
		if onUpdate != nil {
			onUpdate(m)
		}
	}

	phase := "ignoring"
	ignoreDone := 0
	for ignoreDone < ignoreTarget {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		cur := readOnce()
		ignoreDone++
		emit(map[string]interface{}{
			"phase":        phase,
			"ignoreDone":   ignoreDone,
			"ignoreTarget": ignoreTarget,
			"avgDone":      0,
			"avgTarget":    avgTarget,
			"current":      cur,
		})
		time.Sleep(5 * time.Millisecond)
	}

	phase = "averaging"
	avgDone := 0
	sums := make([][]int64, nBars)
	counts := make([][]int64, nBars)
	for i := 0; i < nBars; i++ {
		sums[i] = make([]int64, nLCs)
		counts[i] = make([]int64, nLCs)
	}

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
		emit(map[string]interface{}{
			"phase":        phase,
			"ignoreDone":   ignoreTarget,
			"ignoreTarget": ignoreTarget,
			"avgDone":      avgDone,
			"avgTarget":    avgTarget,
			"current":      cur,
		})
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

	emit(map[string]interface{}{
		"phase": "finished",
		"final": final,
	})

	flat := make([]int64, nBars*nLCs)
	for i := 0; i < nBars; i++ {
		for lc := 0; lc < nLCs; lc++ {
			flat[i*nLCs+lc] = final[i][lc]
		}
	}
	return flat, nil
}

func updateMatrixZero(flat []int64, calibs int, nlcs int) *matrix.Matrix {
	ad := matrix.NewVector(len(flat))
	for i, v := range flat {
		ad.Values[i] = float64(v)
	}
	nbars := len(flat) / nlcs
	ad0 := matrix.NewMatrix(calibs*nlcs, nbars*nlcs)
	for i := 0; i < calibs*nlcs; i++ {
		ad0.SetRow(i, ad)
	}
	return ad0
}

func updateMatrixWeight(adc *matrix.Matrix, flat []int64, index int, nlcs int) *matrix.Matrix {
	nbars := len(flat) / nlcs
	for j := 0; j < nbars; j++ {
		for i := 0; i < nlcs; i++ {
			curr := j*nlcs + i
			adc.Values[index][curr] = float64(flat[curr])
		}
	}
	return adc
}

func computeZerosAndFactors(adv, ad0 *matrix.Matrix, p *models.PARAMETERS) error {
	if adv == nil || ad0 == nil {
		return fmt.Errorf("missing matrices")
	}
	add := adv.Sub(ad0)
	w := matrix.NewVectorWithValue(adv.Rows, float64(p.WEIGHT))
	adi := add.InverseSVD()
	if adi == nil {
		return fmt.Errorf("SVD failed")
	}
	factors := adi.MulVector(w)
	if factors == nil {
		return fmt.Errorf("pseudoinverse multiplication failed")
	}
	zeros := ad0.GetRow(0)
	nbars := len(p.BARS)
	nlcs := zeros.Length / nbars
	for i := 0; i < nbars; i++ {
		p.BARS[i].LC = make([]*models.LC, nlcs)
		for j := 0; j < nlcs; j++ {
			idx := i*nlcs + j
			f := float32(factors.Values[idx])
			p.BARS[i].LC[j] = &models.LC{
				ZERO:   uint64(zeros.Values[idx]),
				FACTOR: f,
				IEEE:   fmt.Sprintf("%08X", matrix.ToIEEE754(f)),
			}
		}
	}
	return nil
}

func ensureFactorsFromDevice(bars *serialpkg.Leo485, p *models.PARAMETERS, configFilename string) error {
	if bars == nil || p == nil {
		return fmt.Errorf("not connected")
	}
	if strings.HasSuffix(strings.ToLower(configFilename), "_calibrated.json") {
		return nil
	}
	for i := 0; i < len(bars.Bars); i++ {
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

func collectAveragedZeros(ctx context.Context, bars *serialpkg.Leo485, p *models.PARAMETERS, samples int, onProgress func(map[string]int)) ([]int64, error) {
	if bars == nil {
		return nil, fmt.Errorf("not connected")
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
	emit := func(m map[string]int) {
		if onProgress != nil {
			onProgress(m)
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
		emit(map[string]int{"warmupDone": w + 1, "warmupTarget": warmup, "sampleDone": 0, "sampleTarget": samples})
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
		emit(map[string]int{"warmupDone": warmup, "warmupTarget": warmup, "sampleDone": s + 1, "sampleTarget": samples})
		time.Sleep(5 * time.Millisecond)
	}
	avg := make([]int64, nb*nlcs)
	if count == 0 {
		return avg, nil
	}
	for i := range sums {
		avg[i] = sums[i] / int64(count)
	}
	return avg, nil
}

func computeTestSnapshot(bars *serialpkg.Leo485, p *models.PARAMETERS, zerosFlat []int64) (map[string]interface{}, error) {
	if bars == nil || p == nil {
		return nil, fmt.Errorf("not connected")
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
	perBarLCWeight := make([][]float64, nb)
	perBarTotal := make([]float64, nb)
	perBarADC := make([][]int64, nb)
	grand := 0.0

	for i := 0; i < nb; i++ {
		ad, err := bars.GetADs(i)
		if err != nil {
			return nil, fmt.Errorf("bar %d read error: %w", i+1, err)
		}
		perBarLCWeight[i] = make([]float64, nlcs)
		perBarADC[i] = make([]int64, nlcs)
		total := 0.0
		for lc := 0; lc < nlcs; lc++ {
			adc := int64(0)
			if lc < len(ad) {
				adc = int64(ad[lc])
			}
			perBarADC[i][lc] = adc
			zero := float64(zerosPerBar[i][lc])
			factor := float64(1)
			if lc < len(p.BARS[i].LC) {
				factor = float64(p.BARS[i].LC[lc].FACTOR)
			}
			w := (float64(adc) - zero) * factor
			perBarLCWeight[i][lc] = w
			total += w
		}
		perBarTotal[i] = total
		grand += total
	}

	return map[string]interface{}{
		"perBarLCWeight": perBarLCWeight,
		"perBarTotal":    perBarTotal,
		"grandTotal":     grand,
		"perBarADC":      perBarADC,
	}, nil
}

