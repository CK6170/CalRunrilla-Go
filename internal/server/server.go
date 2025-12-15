package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/CK6170/Calrunrilla-go/matrix"
	"github.com/CK6170/Calrunrilla-go/models"
	serialpkg "github.com/CK6170/Calrunrilla-go/serial"
)

type DeviceSession struct {
	mu sync.Mutex

	configID string
	params   *models.PARAMETERS
	bars     *serialpkg.Leo485

	// One active operation at a time
	opCancel context.CancelFunc
	opKind   string

	// calibration accumulation
	calMu       sync.Mutex
	calAd0      *matrix.Matrix
	calAdv      *matrix.Matrix
	calReceived int
	calSteps    []CalStep
	calNLoads   int
}

type Server struct {
	mux *http.ServeMux

	store *ConfigStore
	dev   *DeviceSession

	// WebSocket hubs
	wsTest  *WSHub
	wsCal   *WSHub
	wsFlash *WSHub
}

func New() *Server {
	s := &Server{
		mux:     http.NewServeMux(),
		store:   NewConfigStore(),
		dev:     &DeviceSession{},
		wsTest:  NewWSHub(),
		wsCal:   NewWSHub(),
		wsFlash: NewWSHub(),
	}

	// API
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/upload/config", s.handleUploadConfig)
	s.mux.HandleFunc("/api/upload/calibrated", s.handleUploadCalibrated)
	s.mux.HandleFunc("/api/connect", s.handleConnect)
	s.mux.HandleFunc("/api/disconnect", s.handleDisconnect)
	s.mux.HandleFunc("/api/download", s.handleDownload)

	s.mux.HandleFunc("/api/calibration/plan", s.handleCalPlan)
	s.mux.HandleFunc("/api/calibration/startStep", s.handleCalStartStep)
	s.mux.HandleFunc("/api/calibration/stop", s.handleStopOp)

	s.mux.HandleFunc("/api/test/start", s.handleTestStart)
	s.mux.HandleFunc("/api/test/stop", s.handleStopOp)

	s.mux.HandleFunc("/api/flash/start", s.handleFlashStart)
	s.mux.HandleFunc("/api/flash/stop", s.handleStopOp)

	// WS
	s.mux.HandleFunc("/ws/test", s.handleWSTest)
	s.mux.HandleFunc("/ws/calibration", s.handleWSCal)
	s.mux.HandleFunc("/ws/flash", s.handleWSFlash)

	// Static frontend
	s.mux.Handle("/", http.FileServer(http.Dir("./web")))

	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) readJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	s.writeJSON(w, 200, HealthResponse{OK: true, Timestamp: time.Now()})
}

func (s *Server) handleUploadConfig(w http.ResponseWriter, r *http.Request) {
	s.handleUpload(w, r, kindConfig)
}

func (s *Server) handleUploadCalibrated(w http.ResponseWriter, r *http.Request) {
	s.handleUpload(w, r, kindCalibrated)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request, kind configKind) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	f, _, err := fileFromMultipart(r, "file")
	if err != nil {
		s.writeJSON(w, 400, APIError{Error: err.Error()})
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 4<<20))
	if err != nil {
		s.writeJSON(w, 400, APIError{Error: err.Error()})
		return
	}
	p, err := decodeParameters(raw)
	if err != nil {
		s.writeJSON(w, 400, APIError{Error: err.Error()})
		return
	}
	rec, err := s.store.Put(kind, raw, p)
	if err != nil {
		s.writeJSON(w, 500, APIError{Error: err.Error()})
		return
	}
	s.writeJSON(w, 200, UploadResponse{ConfigID: rec.ID, Kind: string(kind)})
}

func fileFromMultipart(r *http.Request, field string) (multipart.File, *multipart.FileHeader, error) {
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		return nil, nil, err
	}
	f, hdr, err := r.FormFile(field)
	if err != nil {
		return nil, nil, err
	}
	return f, hdr, nil
}

func decodeParameters(raw []byte) (*models.PARAMETERS, error) {
	var p models.PARAMETERS
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if p.SERIAL == nil {
		return nil, fmt.Errorf("missing SERIAL in JSON")
	}
	if len(p.BARS) == 0 {
		return nil, fmt.Errorf("no BARS in JSON")
	}
	if p.IGNORE <= 0 {
		p.IGNORE = p.AVG
	}
	return &p, nil
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req ConnectRequest
	if err := s.readJSON(r, &req); err != nil {
		s.writeJSON(w, 400, APIError{Error: err.Error()})
		return
	}
	rec, ok := s.store.Get(req.ConfigID)
	if !ok || rec.Kind != kindConfig {
		s.writeJSON(w, 404, APIError{Error: "configId not found (upload config.json first)"})
		return
	}

	s.dev.mu.Lock()
	defer s.dev.mu.Unlock()

	s.dev.cancelLocked()
	_ = s.dev.disconnectLocked()

	// Ensure port
	if strings.TrimSpace(rec.P.SERIAL.PORT) == "" {
		port := serialpkg.AutoDetectPort(rec.P)
		if port == "" {
			s.writeJSON(w, 400, APIError{Error: "could not auto-detect serial port"})
			return
		}
		rec.P.SERIAL.PORT = port
	}

	bars, err := openBars(rec.P.SERIAL, rec.P.BARS)
	if err != nil {
		s.writeJSON(w, 400, APIError{Error: err.Error()})
		return
	}
	// Probe
	if _, _, _, err := bars.GetVersion(0); err != nil {
		_ = bars.Close()
		s.writeJSON(w, 400, APIError{Error: "device version probe failed: " + err.Error()})
		return
	}

	s.dev.configID = rec.ID
	s.dev.params = rec.P
	s.dev.bars = bars

	s.writeJSON(w, 200, ConnectResponse{
		Connected: true,
		Port:      rec.P.SERIAL.PORT,
		Bars:      len(rec.P.BARS),
		LCs:       bars.NLCs,
	})
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	s.dev.mu.Lock()
	defer s.dev.mu.Unlock()
	s.dev.cancelLocked()
	_ = s.dev.disconnectLocked()
	s.writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleStopOp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	s.dev.mu.Lock()
	defer s.dev.mu.Unlock()
	s.dev.cancelLocked()
	s.writeJSON(w, 200, map[string]bool{"ok": true})
}

func (d *DeviceSession) cancelLocked() {
	if d.opCancel != nil {
		d.opCancel()
		d.opCancel = nil
		d.opKind = ""
	}
}

func (d *DeviceSession) disconnectLocked() error {
	if d.bars != nil {
		_ = d.bars.Close()
	}
	d.bars = nil
	d.params = nil
	d.configID = ""
	return nil
}

func (s *Server) handleCalPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	s.dev.mu.Lock()
	bars := s.dev.bars
	p := s.dev.params
	s.dev.mu.Unlock()
	if bars == nil || p == nil {
		s.writeJSON(w, 400, APIError{Error: "not connected"})
		return
	}
	steps, _, err := buildCalibrationPlan(p, bars.NLCs)
	if err != nil {
		s.writeJSON(w, 500, APIError{Error: err.Error()})
		return
	}
	out := make([]CalStepDTO, 0, len(steps))
	for i, st := range steps {
		out = append(out, CalStepDTO{
			StepIndex: i,
			Kind:      string(st.Kind),
			Label:     st.Label,
			Prompt:    st.Prompt,
		})
	}
	s.writeJSON(w, 200, CalPlanResponse{Steps: out})
}

func (s *Server) handleCalStartStep(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req CalStartStepRequest
	if err := s.readJSON(r, &req); err != nil {
		s.writeJSON(w, 400, APIError{Error: err.Error()})
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
	s.dev.opKind = "calibration"
	bars := s.dev.bars
	p := s.dev.params
	s.dev.mu.Unlock()

	steps, nloads, err := buildCalibrationPlan(p, bars.NLCs)
	if err != nil {
		s.writeJSON(w, 500, APIError{Error: err.Error()})
		return
	}
	if req.StepIndex < 0 || req.StepIndex >= len(steps) {
		s.writeJSON(w, 400, APIError{Error: "invalid stepIndex"})
		return
	}
	step := steps[req.StepIndex]

	// Reset calibration state at first step
	if req.StepIndex == 0 {
		s.dev.calMu.Lock()
		s.dev.calAd0 = nil
		s.dev.calAdv = nil
		s.dev.calSteps = steps
		s.dev.calNLoads = nloads
		s.dev.calReceived = 0
		s.dev.calMu.Unlock()
	}

	go func() {
		flat, err := sampleADCs(ctx, bars, p.IGNORE, p.AVG, func(update map[string]interface{}) {
			s.wsCal.Broadcast(WSMessage{
				Type: "sample",
				Data: update,
			})
		})
		if err != nil {
			s.wsCal.Broadcast(WSMessage{Type: "error", Data: map[string]string{"error": err.Error()}})
			return
		}

		nbars := len(p.BARS)
		nlcs := bars.NLCs
		calibs := 3 * (nbars - 1)

		s.dev.calMu.Lock()
		defer s.dev.calMu.Unlock()

		if step.Kind == CalStepZero {
			s.dev.calAd0 = updateMatrixZero(flat, calibs, nlcs)
			s.dev.calAdv = matrix.NewMatrix(nloads, nbars*nlcs)
		} else if s.dev.calAdv != nil {
			s.dev.calAdv = updateMatrixWeight(s.dev.calAdv, flat, step.Index, nlcs)
		}
		s.dev.calReceived++

		s.wsCal.Broadcast(WSMessage{
			Type: "stepDone",
			Data: map[string]interface{}{
				"stepIndex": req.StepIndex,
				"label":     step.Label,
			},
		})

		if s.dev.calReceived != len(s.dev.calSteps) {
			return
		}

		if s.dev.calAd0 == nil || s.dev.calAdv == nil {
			s.wsCal.Broadcast(WSMessage{Type: "error", Data: map[string]string{"error": "missing calibration matrices"}})
			return
		}

		if err := computeZerosAndFactors(s.dev.calAdv, s.dev.calAd0, p); err != nil {
			s.wsCal.Broadcast(WSMessage{Type: "error", Data: map[string]string{"error": err.Error()}})
			return
		}

		// Store calibrated parameters in memory so the UI can download/flash later.
		rawCal, err := encodeCalibratedJSON(p)
		if err != nil {
			s.wsCal.Broadcast(WSMessage{Type: "error", Data: map[string]string{"error": err.Error()}})
			return
		}
		rec, err := s.store.Put(kindCalibrated, rawCal, p)
		if err != nil {
			s.wsCal.Broadcast(WSMessage{Type: "error", Data: map[string]string{"error": err.Error()}})
			return
		}

		// Flash with progress -> wsFlash? Keep calibration stream for now.
		err = flashParameters(ctx, bars, p, func(progress map[string]interface{}) {
			s.wsCal.Broadcast(WSMessage{Type: "flashProgress", Data: progress})
		})
		if err != nil {
			s.wsCal.Broadcast(WSMessage{Type: "error", Data: map[string]string{"error": err.Error()}})
			return
		}
		s.wsCal.Broadcast(WSMessage{
			Type: "done",
			Data: map[string]interface{}{
				"ok":           true,
				"calibratedId": rec.ID,
			},
		})
	}()

	s.writeJSON(w, 200, map[string]bool{"ok": true})
}

func encodeCalibratedJSON(p *models.PARAMETERS) ([]byte, error) {
	payload := struct {
		SERIAL *models.SERIAL `json:"SERIAL"`
		BARS   []*models.BAR  `json:"BARS"`
		AVG    int           `json:"AVG"`
		IGNORE int           `json:"IGNORE"`
		DEBUG  bool          `json:"DEBUG"`
	}{
		SERIAL: p.SERIAL,
		BARS:   p.BARS,
		AVG:    p.AVG,
		IGNORE: p.IGNORE,
		DEBUG:  p.DEBUG,
	}
	return json.MarshalIndent(payload, "", "  ")
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		s.writeJSON(w, 400, APIError{Error: "missing id"})
		return
	}
	rec, ok := s.store.Get(id)
	if !ok {
		s.writeJSON(w, 404, APIError{Error: "not found"})
		return
	}
	name := "config.json"
	if rec.Kind == kindCalibrated {
		name = "calibrated.json"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(name)))
	w.WriteHeader(200)
	_, _ = w.Write(rec.Raw)
}

