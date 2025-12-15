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

func flashParameters(ctx context.Context, bars *serialpkg.Leo485, p *models.PARAMETERS, onProgress func(map[string]interface{})) error {
	if bars == nil {
		return fmt.Errorf("not connected")
	}
	if p == nil || len(p.BARS) == 0 || len(p.BARS[0].LC) == 0 {
		return fmt.Errorf("missing calibration factors")
	}
	emit := func(m map[string]interface{}) {
		if onProgress != nil {
			onProgress(m)
		}
	}

	emit(map[string]interface{}{"stage": "enter_update", "message": "Entering update mode..."})
	if err := bars.OpenToUpdate(); err != nil {
		return err
	}

	// Some devices respond later; ensure all are ready by repeating Euler handshake per bar.
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

		emit(map[string]interface{}{"stage": "zeros", "barIndex": i, "message": "Flashing zeros..."})

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
		ok := false
		for attempt := 1; attempt <= 3; attempt++ {
			resp, err := serialpkg.UpdateValue(bars.Serial, zeroCmd, 200)
			if err == nil && strings.Contains(resp, "OK") {
				ok = true
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !ok {
			return fmt.Errorf("bar %d: cannot flash zeros", i+1)
		}

		emit(map[string]interface{}{"stage": "factors", "barIndex": i, "message": "Flashing factors..."})

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
		ok = false
		for attempt := 1; attempt <= 3; attempt++ {
			resp, err := serialpkg.UpdateValue(bars.Serial, facCmd, 200)
			if err == nil && strings.Contains(resp, "OK") {
				ok = true
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !ok {
			return fmt.Errorf("bar %d: cannot flash factors", i+1)
		}

		emit(map[string]interface{}{"stage": "reboot", "barIndex": i, "message": "Rebooting..."})
		_ = bars.Reboot(i)
	}

	emit(map[string]interface{}{"stage": "done", "message": "Flashing complete"})
	return nil
}

