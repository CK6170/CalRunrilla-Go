package modern

import (
	"fmt"

	"github.com/CK6170/Calrunrilla-go/matrix"
	"github.com/CK6170/Calrunrilla-go/models"
)

type CalStepKind string

const (
	CalStepZero   CalStepKind = "zero"
	CalStepWeight CalStepKind = "weight"
)

type CalStep struct {
	Kind   CalStepKind
	Index  int    // for weight steps: 0..nloads-1
	Label  string // e.g. [ZERO] or [0001]
	Prompt string
}

func BuildCalibrationPlan(p *models.PARAMETERS, nlcs int) ([]CalStep, int, error) {
	if p == nil {
		return nil, 0, fmt.Errorf("parameters nil")
	}
	if len(p.BARS) == 0 {
		return nil, 0, fmt.Errorf("no bars configured")
	}
	if nlcs <= 0 {
		return nil, 0, fmt.Errorf("nlcs must be > 0")
	}
	// Match existing CLI logic:
	// nloads := 3 * (nbars - 1) * nlcs
	nbars := len(p.BARS)
	nloads := 3 * (nbars - 1) * nlcs
	steps := make([]CalStep, 0, 1+nloads)
	steps = append(steps, CalStep{
		Kind:   CalStepZero,
		Index:  -1,
		Label:  "[ZERO]",
		Prompt: "Clear the Bay(s), then press Enter to start sampling zeros.",
	})
	for j := 0; j < nloads; j++ {
		msg := fmt.Sprintf(
			"Put %d on the %s Bay on the %s side in the %s of the Shelf, then press Enter.",
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

func UpdateMatrixZero(flat []int64, calibs int, nlcs int) *matrix.Matrix {
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

func UpdateMatrixWeight(adc *matrix.Matrix, flat []int64, index int, nlcs int) *matrix.Matrix {
	nbars := len(flat) / nlcs
	for j := 0; j < nbars; j++ {
		for i := 0; i < nlcs; i++ {
			curr := j*nlcs + i
			adc.Values[index][curr] = float64(flat[curr])
		}
	}
	return adc
}

// ComputeZerosAndFactors updates p.BARS[i].LC with ZERO/FACTOR/IEEE (same logic as CLI),
// but does not print anything.
func ComputeZerosAndFactors(adv, ad0 *matrix.Matrix, p *models.PARAMETERS) error {
	if adv == nil || ad0 == nil {
		return fmt.Errorf("missing matrices")
	}
	if p == nil {
		return fmt.Errorf("parameters nil")
	}
	add := adv.Sub(ad0)
	w := matrix.NewVectorWithValue(adv.Rows, float64(p.WEIGHT))
	adi := add.InverseSVD()
	if adi == nil {
		return fmt.Errorf("SVD failed; cannot compute pseudoinverse")
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

