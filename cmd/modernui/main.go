package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/CK6170/Calrunrilla-go/matrix"
	"github.com/CK6170/Calrunrilla-go/modern"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

type screen int

const (
	screenEntry screen = iota
	screenCalibration
	screenTest
	screenFlash
)

type modeStatus int

const (
	statusIdle modeStatus = iota
	statusRunning
	statusDone
	statusError
)

type model struct {
	scr screen

	// entry
	configInput textinput.Model
	flashInput  textinput.Model

	configPath string

	// connection
	sess     *modern.Session
	lastErr  error
	infoLine string

	// calibration state
	calSteps      []modern.CalStep
	calStepIdx    int
	calAdv        *matrix.Matrix
	calAd0        *matrix.Matrix
	calStatus     modeStatus
	calCalibsRows int
	calNLoads     int

	// test state
	testStatus   modeStatus
	testZeros    []int64
	testZeroProg modern.ZeroProgress
	testSnap     *modern.TestSnapshot
	testLastAt   time.Time

	// flash state
	flashStatus modeStatus
	flashProg   modern.FlashProgress

	// cancellation for long-running mode work
	modeCtx    context.Context
	modeCancel context.CancelFunc
	calRunID   int
	testRunID  int
	flashRunID int
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	helpStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
)

func initialModel() model {
	in := textinput.New()
	in.Placeholder = "Path to config.json"
	in.Focus()
	in.CharLimit = 512
	in.Width = 60

	fi := textinput.New()
	fi.Placeholder = "Path to _calibrated.json"
	fi.CharLimit = 512
	fi.Width = 60

	m := model{
		scr:         screenEntry,
		configInput: in,
		flashInput:  fi,
	}
	// support passing config path as arg
	if len(os.Args) > 1 && strings.TrimSpace(os.Args[1]) != "" {
		m.configInput.SetValue(os.Args[1])
		m.configInput.CursorEnd()
	}
	return m
}

type errMsg struct{ err error }
type infoMsg struct{ s string }
type connectedMsg struct {
	sess       *modern.Session
	configPath string
}
type disconnectedMsg struct{}

type calStepDoneMsg struct {
	runID int
	kind modern.CalStepKind
	idx  int
	flat []int64
}
type calFlashDoneMsg struct{ runID int }

type testZeroProgMsg struct {
	runID int
	p     modern.ZeroProgress
}
type testZerosDoneMsg struct {
	runID int
	zeros []int64
}
type testSnapMsg struct {
	runID int
	snap  *modern.TestSnapshot
}
type testPollStoppedMsg struct{ runID int }

type flashDoneMsg struct{ runID int }
type flashStoppedMsg struct{ runID int }

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			_ = m.disconnect()
			return m, tea.Quit
		}

		switch m.scr {
		case screenEntry:
			return m.updateEntryKey(msg)
		case screenCalibration:
			return m.updateCalibrationKey(msg)
		case screenTest:
			return m.updateTestKey(msg)
		case screenFlash:
			return m.updateFlashKey(msg)
		}

	case errMsg:
		m.lastErr = msg.err
		// move mode statuses to error if currently running
		switch m.scr {
		case screenCalibration:
			m.calStatus = statusError
		case screenTest:
			m.testStatus = statusError
		case screenFlash:
			m.flashStatus = statusError
		}
		return m, nil

	case infoMsg:
		m.infoLine = msg.s
		return m, nil

	case connectedMsg:
		m.sess = msg.sess
		m.configPath = msg.configPath
		m.flashInput.SetValue(modern.CalibratedPath(msg.configPath))
		m.infoLine = fmt.Sprintf("Connected on %s (bars=%d lcs=%d)", m.sess.Params.SERIAL.PORT, len(m.sess.Bars.Bars), m.sess.Bars.NLCs)
		m.lastErr = nil
		return m, nil

	case disconnectedMsg:
		m.sess = nil
		m.infoLine = "Disconnected"
		return m, nil

	case calStepDoneMsg:
		if msg.runID != m.calRunID {
			return m, nil
		}
		// incorporate step into matrices
		if m.sess == nil {
			return m, tea.Batch(func() tea.Msg { return errMsg{err: fmt.Errorf("not connected")} })
		}
		nlcs := m.sess.Bars.NLCs
		nbars := len(m.sess.Params.BARS)
		calibs := 3 * (nbars - 1)
		m.calCalibsRows = calibs
		if msg.kind == modern.CalStepZero {
			m.calAd0 = modern.UpdateMatrixZero(msg.flat, calibs, nlcs)
			// allocate adv matrix once we know nloads
			steps, nloads, err := modern.BuildCalibrationPlan(m.sess.Params, nlcs)
			if err != nil {
				return m, func() tea.Msg { return errMsg{err: err} }
			}
			m.calSteps = steps
			m.calNLoads = nloads
			m.calAdv = matrix.NewMatrix(nloads, nbars*nlcs)
		} else {
			m.calAdv = modern.UpdateMatrixWeight(m.calAdv, msg.flat, msg.idx, nlcs)
		}
		m.calStepIdx++
		if m.calStepIdx >= len(m.calSteps) {
			// compute factors and flash+save automatically
			m.calStatus = statusRunning
			return m, m.computeAndFlashCalibrationCmd(m.modeCtx, m.calRunID)
		}
		m.calStatus = statusIdle
		return m, nil

	case calFlashDoneMsg:
		if msg.runID != m.calRunID {
			return m, nil
		}
		m.calStatus = statusDone
		// return to entry as requested
		m.scr = screenEntry
		m.infoLine = "Calibration complete (saved + flashed)."
		return m, nil

	case testZeroProgMsg:
		if msg.runID != m.testRunID {
			return m, nil
		}
		m.testZeroProg = msg.p
		return m, nil

	case testZerosDoneMsg:
		if msg.runID != m.testRunID {
			return m, nil
		}
		m.testZeros = msg.zeros
		m.testStatus = statusRunning
		return m, m.nextTestPollTick(m.modeCtx, m.testRunID)

	case testSnapMsg:
		if msg.runID != m.testRunID || m.scr != screenTest {
			return m, nil
		}
		m.testSnap = msg.snap
		m.testLastAt = time.Now()
		return m, m.nextTestPollTick(m.modeCtx, m.testRunID)

	case testPollStoppedMsg:
		return m, nil

	case flashDoneMsg:
		if msg.runID != m.flashRunID {
			return m, nil
		}
		m.flashStatus = statusDone
		m.scr = screenEntry
		m.infoLine = "Flash complete."
		return m, nil

	case flashStoppedMsg:
		return m, nil
	}

	// default: let inputs update
	switch m.scr {
	case screenEntry:
		var cmd tea.Cmd
		m.configInput, cmd = m.configInput.Update(msg)
		return m, cmd
	case screenFlash:
		var cmd tea.Cmd
		m.flashInput, cmd = m.flashInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Calrunrilla Modern UI") + "\n")
	b.WriteString(helpStyle.Render("Ctrl+C to quit. 'b' to go back from a mode.") + "\n\n")
	if m.infoLine != "" {
		b.WriteString(okStyle.Render(m.infoLine) + "\n")
	}
	if m.lastErr != nil {
		b.WriteString(errStyle.Render("Error: "+m.lastErr.Error()) + "\n")
	}
	b.WriteString("\n")

	switch m.scr {
	case screenEntry:
		b.WriteString(m.viewEntry())
	case screenCalibration:
		b.WriteString(m.viewCalibration())
	case screenTest:
		b.WriteString(m.viewTest())
	case screenFlash:
		b.WriteString(m.viewFlash())
	}
	return b.String()
}

func (m model) viewEntry() string {
	var b strings.Builder
	b.WriteString("Config JSON:\n")
	b.WriteString(m.configInput.View() + "\n\n")
	if m.sess == nil {
		b.WriteString(helpStyle.Render("Enter a config path then press Enter to connect.") + "\n")
		return b.String()
	}
	b.WriteString(okStyle.Render("Connected.") + "\n\n")
	b.WriteString("Select mode:\n")
	b.WriteString("  1) Calibration\n")
	b.WriteString("  2) Test (live weights)\n")
	b.WriteString("  3) Flash (_calibrated.json)\n\n")
	b.WriteString(helpStyle.Render("Press 1/2/3 to start. Press d to disconnect.") + "\n")
	return b.String()
}

func (m model) viewCalibration() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Calibration") + "\n\n")
	if m.sess == nil {
		b.WriteString(errStyle.Render("Not connected.") + "\n")
		return b.String()
	}
	if len(m.calSteps) == 0 {
		b.WriteString("Preparing...\n")
		return b.String()
	}
	step := m.calSteps[m.calStepIdx]
	b.WriteString(step.Label + " " + step.Prompt + "\n\n")
	if m.calStatus == statusRunning {
		b.WriteString("Working...\n")
	} else {
		b.WriteString(helpStyle.Render("Press Enter to start this step. Press b to go back.") + "\n")
	}
	return b.String()
}

func (m model) viewTest() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Test (live weights)") + "\n\n")
	if m.sess == nil {
		b.WriteString(errStyle.Render("Not connected.") + "\n")
		return b.String()
	}
	switch m.testStatus {
	case statusIdle:
		b.WriteString(helpStyle.Render("Press Enter to start (reads factors if needed, collects zeros, then starts live polling).") + "\n")
	case statusRunning:
		if m.testSnap == nil {
			b.WriteString(fmt.Sprintf("Collecting zeros... warmup %d/%d samples %d/%d\n",
				m.testZeroProg.WarmupDone, m.testZeroProg.WarmupTarget, m.testZeroProg.SampleDone, m.testZeroProg.SampleTarget))
			return b.String()
		}
		b.WriteString(fmt.Sprintf("Grand total: %.1f\n\n", m.testSnap.GrandTotal))
		for i := range m.testSnap.PerBarLCWeight {
			b.WriteString(fmt.Sprintf("Bar %d total: %.1f\n", i+1, m.testSnap.PerBarTotal[i]))
			for lc := range m.testSnap.PerBarLCWeight[i] {
				b.WriteString(fmt.Sprintf("  LC %d: W=%7.1f  ADC=%d\n", lc+1, m.testSnap.PerBarLCWeight[i][lc], m.testSnap.PerBarADC[i][lc]))
			}
			b.WriteString("\n")
		}
		b.WriteString(helpStyle.Render("Press z to re-zero. Press b to go back (stops polling).") + "\n")
	default:
		b.WriteString("...\n")
	}
	return b.String()
}

func (m model) viewFlash() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Flash") + "\n\n")
	b.WriteString("Calibrated JSON:\n")
	b.WriteString(m.flashInput.View() + "\n\n")
	if m.flashStatus == statusRunning {
		b.WriteString("Flashing...\n")
	} else {
		b.WriteString(helpStyle.Render("Press Enter to flash factors to device. Press b to go back.") + "\n")
	}
	return b.String()
}

func (m *model) disconnect() error {
	if m.modeCancel != nil {
		m.modeCancel()
		m.modeCancel = nil
	}
	m.modeCtx = nil
	if m.sess != nil {
		_ = m.sess.Close()
		m.sess = nil
	}
	return nil
}

func (m model) updateEntryKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "enter":
		if m.sess == nil {
			path := strings.TrimSpace(m.configInput.Value())
			if path == "" {
				return m, func() tea.Msg { return errMsg{err: fmt.Errorf("config path is empty")} }
			}
			return m, m.connectCmd(path)
		}
		return m, nil
	case "1":
		if m.sess == nil {
			return m, nil
		}
		m.stopMode()
		m.calRunID++
		m.modeCtx, m.modeCancel = context.WithCancel(context.Background())
		m.scr = screenCalibration
		m.calStatus = statusIdle
		m.calStepIdx = 0
		m.calAd0 = nil
		m.calAdv = nil
		steps, _, err := modern.BuildCalibrationPlan(m.sess.Params, m.sess.Bars.NLCs)
		if err != nil {
			return m, func() tea.Msg { return errMsg{err: err} }
		}
		m.calSteps = steps
		return m, nil
	case "2":
		if m.sess == nil {
			return m, nil
		}
		m.stopMode()
		m.testRunID++
		m.modeCtx, m.modeCancel = context.WithCancel(context.Background())
		m.scr = screenTest
		m.testStatus = statusIdle
		m.testSnap = nil
		m.testZeros = nil
		m.testZeroProg = modern.ZeroProgress{}
		return m, nil
	case "3":
		if m.sess == nil {
			return m, nil
		}
		m.stopMode()
		m.flashRunID++
		m.modeCtx, m.modeCancel = context.WithCancel(context.Background())
		m.scr = screenFlash
		m.flashStatus = statusIdle
		if strings.TrimSpace(m.flashInput.Value()) == "" && m.configPath != "" {
			m.flashInput.SetValue(modern.CalibratedPath(m.configPath))
		}
		m.flashInput.CursorEnd()
		return m, nil
	case "d":
		_ = m.disconnect()
		return m, func() tea.Msg { return disconnectedMsg{} }
	}

	var cmd tea.Cmd
	m.configInput, cmd = m.configInput.Update(k)
	return m, cmd
}

func (m model) updateCalibrationKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "b":
		m.stopMode()
		m.calRunID++
		m.scr = screenEntry
		m.calStatus = statusIdle
		return m, nil
	case "enter":
		if m.calStatus == statusRunning {
			return m, nil
		}
		if m.sess == nil {
			return m, func() tea.Msg { return errMsg{err: fmt.Errorf("not connected")} }
		}
		if m.calStepIdx >= len(m.calSteps) {
			return m, nil
		}
		step := m.calSteps[m.calStepIdx]
		m.calStatus = statusRunning
		return m, m.runCalibrationStepCmd(m.modeCtx, m.calRunID, step)
	}
	return m, nil
}

func (m model) updateTestKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "b":
		m.stopMode()
		m.testRunID++
		m.scr = screenEntry
		m.testStatus = statusIdle
		return m, nil
	case "enter":
		if m.testStatus == statusRunning {
			return m, nil
		}
		if m.sess == nil {
			return m, func() tea.Msg { return errMsg{err: fmt.Errorf("not connected")} }
		}
		m.testStatus = statusRunning
		return m, m.startTestModeCmd(m.modeCtx, m.testRunID)
	case "z":
		if m.testStatus != statusRunning || m.sess == nil {
			return m, nil
		}
		// cancel polling, re-zero, then resume polling
		m.stopMode()
		m.testRunID++
		m.modeCtx, m.modeCancel = context.WithCancel(context.Background())
		m.testSnap = nil
		m.testStatus = statusRunning
		return m, m.collectZerosCmd(m.modeCtx, m.testRunID)
	}
	return m, nil
}

func (m model) updateFlashKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "b":
		m.stopMode()
		m.flashRunID++
		m.scr = screenEntry
		m.flashStatus = statusIdle
		return m, nil
	case "enter":
		if m.flashStatus == statusRunning {
			return m, nil
		}
		if m.sess == nil {
			return m, func() tea.Msg { return errMsg{err: fmt.Errorf("not connected")} }
		}
		path := strings.TrimSpace(m.flashInput.Value())
		if path == "" {
			return m, func() tea.Msg { return errMsg{err: fmt.Errorf("calibrated json path is empty")} }
		}
		m.flashStatus = statusRunning
		return m, m.flashFromFileCmd(m.modeCtx, m.flashRunID, path)
	}
	var cmd tea.Cmd
	m.flashInput, cmd = m.flashInput.Update(k)
	return m, cmd
}

func (m *model) stopMode() {
	if m.modeCancel != nil {
		m.modeCancel()
		m.modeCancel = nil
	}
	m.modeCtx = nil
}

func (m model) connectCmd(path string) tea.Cmd {
	return func() tea.Msg {
		p, err := modern.LoadParameters(path)
		if err != nil {
			return errMsg{err: err}
		}
		_, err = modern.EnsureSerialPort(path, p, true)
		if err != nil {
			return errMsg{err: err}
		}
		sess, err := modern.Connect(p)
		if err != nil {
			return errMsg{err: err}
		}
		if err := modern.ProbeVersion(sess); err != nil {
			_ = sess.Close()
			return errMsg{err: err}
		}
		return connectedMsg{sess: sess, configPath: path}
	}
}

func (m model) runCalibrationStepCmd(ctx context.Context, runID int, step modern.CalStep) tea.Cmd {
	return func() tea.Msg {
		if m.sess == nil {
			return errMsg{err: fmt.Errorf("not connected")}
		}
		if ctx == nil {
			return errMsg{err: fmt.Errorf("mode context not set")}
		}
		ignore := m.sess.Params.IGNORE
		avg := m.sess.Params.AVG
		flat, err := modern.SampleADCs(ctx, m.sess.Bars, ignore, avg, nil)
		if err != nil {
			return errMsg{err: err}
		}
		return calStepDoneMsg{runID: runID, kind: step.Kind, idx: step.Index, flat: flat}
	}
}

func (m model) computeAndFlashCalibrationCmd(ctx context.Context, runID int) tea.Cmd {
	return func() tea.Msg {
		if m.sess == nil {
			return errMsg{err: fmt.Errorf("not connected")}
		}
		if ctx == nil {
			return errMsg{err: fmt.Errorf("mode context not set")}
		}
		if err := modern.ComputeZerosAndFactors(m.calAdv, m.calAd0, m.sess.Params); err != nil {
			return errMsg{err: err}
		}
		calPath := modern.CalibratedPath(m.configPath)
		if err := modern.SaveCalibratedJSON(calPath, m.sess.Params); err != nil {
			return errMsg{err: err}
		}
		if err := modern.FlashParameters(ctx, m.sess.Bars, m.sess.Params, nil); err != nil {
			return errMsg{err: err}
		}
		return calFlashDoneMsg{runID: runID}
	}
}

func (m model) startTestModeCmd(ctx context.Context, runID int) tea.Cmd {
	return func() tea.Msg {
		if m.sess == nil {
			return errMsg{err: fmt.Errorf("not connected")}
		}
		if ctx == nil {
			return errMsg{err: fmt.Errorf("mode context not set")}
		}
		if err := modern.EnsureFactorsFromDevice(ctx, m.sess.Bars, m.sess.Params, m.configPath); err != nil {
			return errMsg{err: err}
		}
		zeros, err := modern.CollectAveragedZeros(ctx, m.sess.Bars, m.sess.Params, m.sess.Params.AVG, nil)
		if err != nil {
			return errMsg{err: err}
		}
		return testZerosDoneMsg{runID: runID, zeros: zeros}
	}
}

func (m model) collectZerosCmd(ctx context.Context, runID int) tea.Cmd {
	return func() tea.Msg {
		if m.sess == nil {
			return errMsg{err: fmt.Errorf("not connected")}
		}
		if ctx == nil {
			return errMsg{err: fmt.Errorf("mode context not set")}
		}
		zeros, err := modern.CollectAveragedZeros(ctx, m.sess.Bars, m.sess.Params, m.sess.Params.AVG, nil)
		if err != nil {
			return errMsg{err: err}
		}
		return testZerosDoneMsg{runID: runID, zeros: zeros}
	}
}

func (m model) nextTestPollTick(ctx context.Context, runID int) tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
		if ctx == nil {
			return testPollStoppedMsg{runID: runID}
		}
		select {
		case <-ctx.Done():
			return testPollStoppedMsg{runID: runID}
		default:
		}
		if m.sess == nil {
			return errMsg{err: fmt.Errorf("not connected")}
		}
		snap, err := modern.ComputeTestSnapshot(m.sess.Bars, m.sess.Params, m.testZeros)
		if err != nil {
			return errMsg{err: err}
		}
		return testSnapMsg{runID: runID, snap: snap}
	})
}

func (m model) flashFromFileCmd(ctx context.Context, runID int, path string) tea.Cmd {
	return func() tea.Msg {
		if m.sess == nil {
			return errMsg{err: fmt.Errorf("not connected")}
		}
		if ctx == nil {
			return errMsg{err: fmt.Errorf("mode context not set")}
		}
		p, err := modern.LoadParameters(path)
		if err != nil {
			return errMsg{err: err}
		}
		if err := modern.FlashParameters(ctx, m.sess.Bars, p, nil); err != nil {
			return errMsg{err: err}
		}
		return flashDoneMsg{runID: runID}
	}
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
}

