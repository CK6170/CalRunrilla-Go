package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/CK6170/Calrunrilla-go/matrix"
	"github.com/CK6170/Calrunrilla-go/modern"
	serialpkg "github.com/CK6170/Calrunrilla-go/serial"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx context.Context

	mu sync.Mutex

	configPath string
	sess       *modern.Session

	// background operation cancellation (test/flash/calibration)
	opCancel context.CancelFunc
	opKind   string

	// calibration accumulation
	calMu       sync.Mutex
	calAd0      *matrix.Matrix
	calAdv      *matrix.Matrix
	calNLoads   int
	calReceived int
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

type ConnectionInfo struct {
	ConfigPath string `json:"configPath"`
	Port       string `json:"port"`
	Bars       int    `json:"bars"`
	LCs        int    `json:"lcs"`
}

type FlashProgressDTO struct {
	Stage    string `json:"stage"`
	BarIndex int    `json:"barIndex"`
	Message  string `json:"message"`
}

type TestSnapshotDTO struct {
	PerBarLCWeight [][]float64 `json:"perBarLCWeight"`
	PerBarTotal    []float64   `json:"perBarTotal"`
	GrandTotal     float64     `json:"grandTotal"`
	PerBarADC      [][]int64   `json:"perBarADC"`
}

type ZeroProgressDTO struct {
	WarmupDone   int `json:"warmupDone"`
	WarmupTarget int `json:"warmupTarget"`
	SampleDone   int `json:"sampleDone"`
	SampleTarget int `json:"sampleTarget"`
}

type SampleProgressDTO struct {
	Phase        string    `json:"phase"`
	IgnoreDone   int       `json:"ignoreDone"`
	IgnoreTarget int       `json:"ignoreTarget"`
	AvgDone      int       `json:"avgDone"`
	AvgTarget    int       `json:"avgTarget"`
	Current      [][]int64 `json:"current,omitempty"`
	Final        [][]int64 `json:"final,omitempty"`
}

type CalStepDTO struct {
	Kind   string `json:"kind"`
	Index  int    `json:"index"`
	Label  string `json:"label"`
	Prompt string `json:"prompt"`
}

// Connect loads the config JSON, auto-detects the port if needed, and connects to the device.
func (a *App) Connect(configPath string) (*ConnectionInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.cancelLocked()
	_ = a.disconnectLocked()

	p, err := modern.LoadParameters(configPath)
	if err != nil {
		return nil, err
	}
	_, err = modern.EnsureSerialPort(configPath, p, true)
	if err != nil {
		return nil, err
	}
	sess, err := modern.Connect(p)
	if err != nil {
		return nil, err
	}
	if err := modern.ProbeVersion(sess); err != nil {
		_ = sess.Close()
		return nil, err
	}

	a.configPath = configPath
	a.sess = sess

	ci := &ConnectionInfo{
		ConfigPath: configPath,
		Port:       sess.Params.SERIAL.PORT,
		Bars:       len(sess.Bars.Bars),
		LCs:        sess.Bars.NLCs,
	}
	runtime.EventsEmit(a.ctx, "device:connected", ci)
	return ci, nil
}

func (a *App) Disconnect() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancelLocked()
	err := a.disconnectLocked()
	runtime.EventsEmit(a.ctx, "device:disconnected", nil)
	return err
}

func (a *App) CancelOperation() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancelLocked()
}

func (a *App) cancelLocked() {
	if a.opCancel != nil {
		a.opCancel()
		a.opCancel = nil
		a.opKind = ""
	}
}

func (a *App) disconnectLocked() error {
	if a.sess == nil {
		return nil
	}
	err := a.sess.Close()
	a.sess = nil
	a.configPath = ""
	return err
}

func (a *App) GetCalibrationPlan() ([]CalStepDTO, error) {
	a.mu.Lock()
	sess := a.sess
	a.mu.Unlock()
	if sess == nil {
		return nil, fmt.Errorf("not connected")
	}
	steps, _, err := modern.BuildCalibrationPlan(sess.Params, sess.Bars.NLCs)
	if err != nil {
		return nil, err
	}
	out := make([]CalStepDTO, 0, len(steps))
	for _, s := range steps {
		out = append(out, CalStepDTO{
			Kind:   string(s.Kind),
			Index:  s.Index,
			Label:  s.Label,
			Prompt: s.Prompt,
		})
	}
	return out, nil
}

// StartFlash reads a _calibrated.json and flashes it to the connected device.
func (a *App) StartFlash(calibratedPath string) error {
	a.mu.Lock()
	if a.sess == nil {
		a.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	a.cancelLocked()
	ctx, cancel := context.WithCancel(context.Background())
	a.opCancel = cancel
	a.opKind = "flash"
	sess := a.sess
	a.mu.Unlock()

	go func() {
		p, err := modern.LoadParameters(calibratedPath)
		if err != nil {
			runtime.EventsEmit(a.ctx, "flash:error", err.Error())
			return
		}
		err = modern.FlashParameters(ctx, sess.Bars, p, func(pr modern.FlashProgress) {
			runtime.EventsEmit(a.ctx, "flash:progress", FlashProgressDTO{
				Stage:    string(pr.Stage),
				BarIndex: pr.BarIndex,
				Message:  pr.Message,
			})
		})
		if err != nil {
			runtime.EventsEmit(a.ctx, "flash:error", err.Error())
			return
		}
		runtime.EventsEmit(a.ctx, "flash:done", nil)
	}()
	return nil
}

// StartTest starts live polling (weights + ADC) and streams snapshots to the UI.
func (a *App) StartTest() error {
	a.mu.Lock()
	if a.sess == nil {
		a.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	a.cancelLocked()
	ctx, cancel := context.WithCancel(context.Background())
	a.opCancel = cancel
	a.opKind = "test"
	sess := a.sess
	configPath := a.configPath
	a.mu.Unlock()

	go func() {
		if err := modern.EnsureFactorsFromDevice(ctx, sess.Bars, sess.Params, configPath); err != nil {
			runtime.EventsEmit(a.ctx, "test:error", err.Error())
			return
		}
		zeros, err := modern.CollectAveragedZeros(ctx, sess.Bars, sess.Params, sess.Params.AVG, func(z modern.ZeroProgress) {
			runtime.EventsEmit(a.ctx, "test:zerosProgress", ZeroProgressDTO{
				WarmupDone:   z.WarmupDone,
				WarmupTarget: z.WarmupTarget,
				SampleDone:   z.SampleDone,
				SampleTarget: z.SampleTarget,
			})
		})
		if err != nil {
			runtime.EventsEmit(a.ctx, "test:error", err.Error())
			return
		}
		runtime.EventsEmit(a.ctx, "test:zerosDone", nil)

		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				runtime.EventsEmit(a.ctx, "test:stopped", nil)
				return
			case <-t.C:
				snap, err := modern.ComputeTestSnapshot(sess.Bars, sess.Params, zeros)
				if err != nil {
					runtime.EventsEmit(a.ctx, "test:error", err.Error())
					return
				}
				runtime.EventsEmit(a.ctx, "test:snapshot", TestSnapshotDTO{
					PerBarLCWeight: snap.PerBarLCWeight,
					PerBarTotal:    snap.PerBarTotal,
					GrandTotal:     snap.GrandTotal,
					PerBarADC:      snap.PerBarADC,
				})
			}
		}
	}()
	return nil
}

func (a *App) StopTest() {
	a.CancelOperation()
}

// StartCalibrationStep samples one calibration step (zero or weight position).
// It emits:
// - calibration:sample (live updates)
// - calibration:stepDone (final avg for that step)
// - calibration:done (when final step computed+flashed)
func (a *App) StartCalibrationStep(stepIndex int) error {
	a.mu.Lock()
	if a.sess == nil {
		a.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	a.cancelLocked()
	ctx, cancel := context.WithCancel(context.Background())
	a.opCancel = cancel
	a.opKind = "calibration"
	sess := a.sess
	configPath := a.configPath
	a.mu.Unlock()

	go func() {
		steps, nloads, err := modern.BuildCalibrationPlan(sess.Params, sess.Bars.NLCs)
		if err != nil {
			runtime.EventsEmit(a.ctx, "calibration:error", err.Error())
			return
		}
		if stepIndex < 0 || stepIndex >= len(steps) {
			runtime.EventsEmit(a.ctx, "calibration:error", fmt.Sprintf("invalid stepIndex %d", stepIndex))
			return
		}

		step := steps[stepIndex]
		flat, err := modern.SampleADCs(ctx, sess.Bars, sess.Params.IGNORE, sess.Params.AVG, func(u modern.SampleUpdate) {
			runtime.EventsEmit(a.ctx, "calibration:sample", SampleProgressDTO{
				Phase:        string(u.Phase),
				IgnoreDone:   u.IgnoreDone,
				IgnoreTarget: u.IgnoreTarget,
				AvgDone:      u.AvgDone,
				AvgTarget:    u.AvgTarget,
				Current:      u.Current,
				Final:        u.Final,
			})
		})
		if err != nil {
			runtime.EventsEmit(a.ctx, "calibration:error", err.Error())
			return
		}

		// Update calibration matrices incrementally (same math as CLI).
		nbars := len(sess.Params.BARS)
		nlcs := sess.Bars.NLCs
		calibs := 3 * (nbars - 1)

		a.calMu.Lock()
		defer a.calMu.Unlock()

		// Reset on first step
		if stepIndex == 0 {
			a.calAd0 = nil
			a.calAdv = nil
			a.calNLoads = nloads
			a.calReceived = 0
		}

		if step.Kind == modern.CalStepZero {
			a.calAd0 = modern.UpdateMatrixZero(flat, calibs, nlcs)
			a.calAdv = matrix.NewMatrix(nloads, nbars*nlcs)
		} else {
			// weight steps start at 1 in plan, but their Index is 0..nloads-1
			if a.calAdv != nil {
				a.calAdv = modern.UpdateMatrixWeight(a.calAdv, flat, step.Index, nlcs)
			}
		}
		a.calReceived++

		runtime.EventsEmit(a.ctx, "calibration:stepDone", map[string]interface{}{
			"stepIndex": stepIndex,
			"label":     step.Label,
		})

		// If all steps collected, compute factors + save calibrated json + flash.
		if a.calReceived != len(steps) {
			return
		}
		if a.calAd0 == nil || a.calAdv == nil {
			runtime.EventsEmit(a.ctx, "calibration:error", "missing calibration matrices")
			return
		}
		if err := modern.ComputeZerosAndFactors(a.calAdv, a.calAd0, sess.Params); err != nil {
			runtime.EventsEmit(a.ctx, "calibration:error", err.Error())
			return
		}
		calPath := modern.CalibratedPath(configPath)
		if err := modern.SaveCalibratedJSON(calPath, sess.Params); err != nil {
			runtime.EventsEmit(a.ctx, "calibration:error", err.Error())
			return
		}
		// flash
		if err := modern.FlashParameters(ctx, sess.Bars, sess.Params, func(pr modern.FlashProgress) {
			runtime.EventsEmit(a.ctx, "calibration:flashProgress", FlashProgressDTO{
				Stage:    string(pr.Stage),
				BarIndex: pr.BarIndex,
				Message:  pr.Message,
			})
		}); err != nil {
			runtime.EventsEmit(a.ctx, "calibration:error", err.Error())
			return
		}
		runtime.EventsEmit(a.ctx, "calibration:done", map[string]interface{}{
			"calibratedPath": calPath,
			"calibratedFile": filepath.Base(calPath),
		})
	}()
	return nil
}

// AutoDetectPort is exposed so the UI can show the detected port without connecting.
func (a *App) AutoDetectPort(configPath string) (string, error) {
	p, err := modern.LoadParameters(configPath)
	if err != nil {
		return "", err
	}
	// do not persist for preview
	changed, err := modern.EnsureSerialPort(configPath, p, false)
	if err != nil {
		return "", err
	}
	if !changed {
		return p.SERIAL.PORT, nil
	}
	return p.SERIAL.PORT, nil
}

// ListSerialCandidates (best-effort) uses the same heuristics as the auto-detect.
// (Note: current serial package auto-detect probes ports; this returns just the chosen one.)
func (a *App) ListSerialCandidates(configPath string) ([]string, error) {
	p, err := modern.LoadParameters(configPath)
	if err != nil {
		return nil, err
	}
	_ = p
	// placeholder for future: could enumerate OS ports without probing.
	return []string{}, nil
}

// ReReadFactors asks the device for stored factors and updates the current session params.
func (a *App) ReReadFactors() error {
	a.mu.Lock()
	sess := a.sess
	configPath := a.configPath
	a.mu.Unlock()
	if sess == nil {
		return fmt.Errorf("not connected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return modern.EnsureFactorsFromDevice(ctx, sess.Bars, sess.Params, configPath)
}

// ReadFactorsRaw reads factors from the device for a given bar index (debug helper).
func (a *App) ReadFactorsRaw(barIndex int) ([]float64, error) {
	a.mu.Lock()
	sess := a.sess
	a.mu.Unlock()
	if sess == nil {
		return nil, fmt.Errorf("not connected")
	}
	if barIndex < 0 || barIndex >= len(sess.Bars.Bars) {
		return nil, fmt.Errorf("invalid barIndex")
	}
	return sess.Bars.ReadFactors(barIndex)
}

// SendVersion is a debug helper that queries the version string.
func (a *App) SendVersion() (string, error) {
	a.mu.Lock()
	sess := a.sess
	a.mu.Unlock()
	if sess == nil {
		return "", fmt.Errorf("not connected")
	}
	id, maj, min, err := sess.Bars.GetVersion(0)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("ID=%d %d.%d", id, maj, min), nil
}

// BuildCommand debug helper (kept internal; used during troubleshooting).
func (a *App) BuildCommand(barID int, cmd string) []byte {
	return serialpkg.GetCommand(barID, []byte(cmd))
}
