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

type FlashStage string

const (
	FlashStageEnterUpdate FlashStage = "enter_update"
	FlashStageZeros       FlashStage = "zeros"
	FlashStageFactors     FlashStage = "factors"
	FlashStageReboot      FlashStage = "reboot"
	FlashStageDone        FlashStage = "done"
)

type FlashProgress struct {
	Stage    FlashStage
	BarIndex int // 0-based
	Message  string
}

func FlashParameters(ctx context.Context, bars *serialpkg.Leo485, p *models.PARAMETERS, onProgress func(FlashProgress)) error {
	if bars == nil {
		return fmt.Errorf("bars not connected")
	}
	if p == nil || len(p.BARS) == 0 {
		return fmt.Errorf("no bars configured")
	}
	if len(p.BARS[0].LC) == 0 {
		return fmt.Errorf("no calibration factors present (BARS[0].LC empty)")
	}

	emit := func(pr FlashProgress) {
		if onProgress != nil {
			onProgress(pr)
		}
	}

	emit(FlashProgress{Stage: FlashStageEnterUpdate, BarIndex: -1, Message: "Entering update mode..."})
	if err := bars.OpenToUpdate(); err != nil {
		// Recovery: reboot all bars and retry once (matching CLI behavior).
		for i := range bars.Bars {
			_ = ctx.Err()
			bars.Reboot(i)
			time.Sleep(100 * time.Millisecond)
		}
		time.Sleep(1500 * time.Millisecond)
		if err2 := bars.OpenToUpdate(); err2 != nil {
			return fmt.Errorf("cannot enter update mode: %v; retry: %v", err, err2)
		}
	}

	// Wait for "Enter" from all bars (matching calibration.flashParameters).
	notReady := make([]int, 0, len(p.BARS))
	for i := 0; i < len(p.BARS); i++ {
		notReady = append(notReady, i)
	}
	for attempt := 1; attempt <= 6 && len(notReady) > 0; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		remaining := make([]int, 0)
		for _, idx := range notReady {
			cmd := serialpkg.GetCommand(p.BARS[idx].ID, []byte(serialpkg.Euler))
			resp, err := serialpkg.ChangeState(bars.Serial, cmd, 400)
			if err != nil || !strings.Contains(resp, "Enter") {
				remaining = append(remaining, idx)
				continue
			}
		}
		notReady = remaining
		if len(notReady) > 0 {
			time.Sleep(500 * time.Millisecond)
		}
	}
	if len(notReady) > 0 {
		return fmt.Errorf("not all bars entered update mode: still missing %v", notReady)
	}

	// Prime bootloaders
	_, _ = bars.Serial.Write([]byte{0x0D})
	_, _ = serialpkg.ReadUntil(bars.Serial, 50)

	nbars := len(p.BARS)
	for i := 0; i < nbars; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		emit(FlashProgress{Stage: FlashStageZeros, BarIndex: i, Message: "Flashing zeros..."})

		nlcs := len(p.BARS[i].LC)
		zero := matrix.NewVector(nlcs)
		facs := matrix.NewVector(nlcs)
		zeravg := 0.0
		for j := 0; j < nlcs; j++ {
			zero.Values[j] = float64(p.BARS[i].LC[j].ZERO)
			facs.Values[j] = float64(p.BARS[i].LC[j].FACTOR)
			zeravg += zero.Values[j] * facs.Values[j]
		}
		if zeravg < 0 {
			zeravg = 0
		}

		// Build zeros payload
		sb := "O"
		k := 0
		for ii := 0; ii < 4; ii++ {
			if (p.BARS[i].LCS & (1 << ii)) != 0 {
				sb += fmt.Sprintf("%09.0f|", zero.Values[k])
				k++
			} else {
				sb += fmt.Sprintf("%09d|", 0)
			}
		}
		sb += fmt.Sprintf("%09d|", uint64(zeravg/float64(nlcs)+0.5))
		zeroCmd := serialpkg.GetCommand(p.BARS[i].ID, []byte(sb))
		wroteZeros := false
		for attempt := 1; attempt <= 3; attempt++ {
			resp, err := serialpkg.UpdateValue(bars.Serial, zeroCmd, 200)
			if err == nil && strings.Contains(resp, "OK") {
				wroteZeros = true
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !wroteZeros {
			return fmt.Errorf("bar %d: cannot flash zeros", i+1)
		}

		emit(FlashProgress{Stage: FlashStageFactors, BarIndex: i, Message: "Flashing factors..."})
		// Build factors payload
		sb2 := "X"
		k2 := 0
		for ii := 0; ii < 4; ii++ {
			if (p.BARS[i].LCS & (1 << ii)) != 0 {
				sb2 += fmt.Sprintf("%.10f|", facs.Values[k2])
				k2++
			} else {
				sb2 += "1.0000000000|"
			}
		}
		facCmd := serialpkg.GetCommand(p.BARS[i].ID, []byte(sb2))
		wroteFacs := false
		for attempt := 1; attempt <= 3; attempt++ {
			resp, err := serialpkg.UpdateValue(bars.Serial, facCmd, 200)
			if err == nil && strings.Contains(resp, "OK") {
				wroteFacs = true
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !wroteFacs {
			return fmt.Errorf("bar %d: cannot flash factors", i+1)
		}

		emit(FlashProgress{Stage: FlashStageReboot, BarIndex: i, Message: "Rebooting..."})
		_ = bars.Reboot(i)
	}

	emit(FlashProgress{Stage: FlashStageDone, BarIndex: -1, Message: "Flashing complete"})
	return nil
}

