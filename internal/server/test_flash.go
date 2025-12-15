package server

import (
	"context"
	"net/http"
	"time"

	"github.com/CK6170/Calrunrilla-go/modern"
)

func (s *Server) handleTestStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	s.dev.mu.Lock()
	if s.dev.bars == nil || s.dev.params == nil {
		s.dev.mu.Unlock()
		s.writeJSON(w, 400, APIError{Error: "not connected"})
		return
	}
	s.dev.cancelLocked()
	ctx, cancel := context.WithCancel(context.Background())
	s.dev.opCancel = cancel
	s.dev.opKind = "test"
	bars := s.dev.bars
	p := s.dev.params
	configID := s.dev.configID
	s.dev.mu.Unlock()

	go func() {
		// Note: we don't have the original filename here; pass a dummy ".json" so it reads factors from device if needed.
		_ = configID
		if err := modern.EnsureFactorsFromDevice(ctx, bars, p, "config.json"); err != nil {
			s.wsTest.Broadcast(WSMessage{Type: "error", Data: map[string]string{"error": err.Error()}})
			return
		}
		zeros, err := modern.CollectAveragedZeros(ctx, bars, p, p.AVG, func(z modern.ZeroProgress) {
			s.wsTest.Broadcast(WSMessage{
				Type: "zerosProgress",
				Data: map[string]int{
					"warmupDone":   z.WarmupDone,
					"warmupTarget": z.WarmupTarget,
					"sampleDone":   z.SampleDone,
					"sampleTarget": z.SampleTarget,
				},
			})
		})
		if err != nil {
			s.wsTest.Broadcast(WSMessage{Type: "error", Data: map[string]string{"error": err.Error()}})
			return
		}
		s.wsTest.Broadcast(WSMessage{Type: "zerosDone"})

		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				s.wsTest.Broadcast(WSMessage{Type: "stopped"})
				return
			case <-t.C:
				snap, err := modern.ComputeTestSnapshot(bars, p, zeros)
				if err != nil {
					s.wsTest.Broadcast(WSMessage{Type: "error", Data: map[string]string{"error": err.Error()}})
					return
				}
				s.wsTest.Broadcast(WSMessage{
					Type: "snapshot",
					Data: map[string]interface{}{
						"perBarLCWeight": snap.PerBarLCWeight,
						"perBarTotal":    snap.PerBarTotal,
						"grandTotal":     snap.GrandTotal,
						"perBarADC":      snap.PerBarADC,
					},
				})
			}
		}
	}()

	s.writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleFlashStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req FlashStartRequest
	if err := s.readJSON(r, &req); err != nil {
		s.writeJSON(w, 400, APIError{Error: err.Error()})
		return
	}
	rec, ok := s.store.Get(req.CalibratedID)
	if !ok || rec.Kind != kindCalibrated {
		s.writeJSON(w, 404, APIError{Error: "calibratedId not found (upload _calibrated.json first)"})
		return
	}

	s.dev.mu.Lock()
	if s.dev.bars == nil {
		s.dev.mu.Unlock()
		s.writeJSON(w, 400, APIError{Error: "not connected"})
		return
	}
	s.dev.cancelLocked()
	ctx, cancel := context.WithCancel(context.Background())
	s.dev.opCancel = cancel
	s.dev.opKind = "flash"
	bars := s.dev.bars
	s.dev.mu.Unlock()

	go func() {
		err := modern.FlashParameters(ctx, bars, rec.P, func(pr modern.FlashProgress) {
			s.wsFlash.Broadcast(WSMessage{
				Type: "progress",
				Data: map[string]interface{}{
					"stage":    string(pr.Stage),
					"barIndex": pr.BarIndex,
					"message":  pr.Message,
				},
			})
		})
		if err != nil {
			s.wsFlash.Broadcast(WSMessage{Type: "error", Data: map[string]string{"error": err.Error()}})
			return
		}
		s.wsFlash.Broadcast(WSMessage{Type: "done"})
	}()

	s.writeJSON(w, 200, map[string]bool{"ok": true})
}

