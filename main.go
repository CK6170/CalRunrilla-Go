package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/tarm/serial"
)

// redWriter wraps an io.Writer and emits red-colored output. Defined at package scope
// because methods cannot be declared inside functions.
type redWriter struct{ w io.Writer }

func (r redWriter) Write(p []byte) (int, error) {
	out := append([]byte("\033[31m"), p...)
	out = append(out, []byte("\033[0m")...)
	return r.w.Write(out)
}

// debugPrintf prints a yellow-colored formatted line when enabled is true.
func debugPrintf(enabled bool, format string, a ...interface{}) {
	if enabled {
		fmt.Print("\033[33m")
		fmt.Printf("[DEBUG] "+format, a...)
		fmt.Print("\033[0m")
	}
}

// greenPrintf prints a light-green formatted line (always shown)
func greenPrintf(format string, a ...interface{}) {
	fmt.Print("\033[92m")
	fmt.Printf(format, a...)
	fmt.Print("\033[0m")
}

const (
	MAXLCS   = 4
	WIDTH    = 21
	SHIFT    = 14
	SHIFTIDX = 6
)

// Raw single-key input (Windows & other OS) using golang.org/x/term
// We switch stdin to raw mode and read bytes directly without needing Enter.

type LMR int

const (
	LEFT LMR = iota
	MIDDLE
	RIGHT
)

func (l LMR) String() string {
	switch l {
	case LEFT:
		return "LEFT"
	case MIDDLE:
		return "MIDDLE"
	case RIGHT:
		return "RIGHT"
	default:
		return fmt.Sprintf("LMR(%d)", int(l))
	}
}

type FB int

const (
	FRONT FB = iota
	BACK
)

func (f FB) String() string {
	switch f {
	case FRONT:
		return "FRONT"
	case BACK:
		return "BACK"
	default:
		return fmt.Sprintf("FB(%d)", int(f))
	}
}

type BAY int

const (
	FIRST BAY = iota
	SECOND
	THIRD
	FOURTH
	FIFTH
	SIXTH
	SEVENTH
	EIGHTH
)

func (b BAY) String() string {
	switch b {
	case FIRST:
		return "FIRST"
	case SECOND:
		return "SECOND"
	case THIRD:
		return "THIRD"
	case FOURTH:
		return "FOURTH"
	case FIFTH:
		return "FIFTH"
	case SIXTH:
		return "SIXTH"
	case SEVENTH:
		return "SEVENTH"
	case EIGHTH:
		return "EIGHTH"
	default:
		return fmt.Sprintf("BAY(%d)", int(b))
	}
}

type PARAMETERS struct {
	SERIAL  *SERIAL  `json:"SERIAL"`
	VERSION *VERSION `json:"VERSION,omitempty"`
	WEIGHT  int      `json:"WEIGHT"`
	AVG     int      `json:"AVG"`              // number of samples to average
	IGNORE  int      `json:"IGNORE,omitempty"` // number of samples to ignore first
	DEBUG   bool     `json:"DEBUG"`
	BARS    []*BAR   `json:"BARS"`
}

type SENTINEL struct {
	SERIAL *SERIAL `json:"SERIAL"`
	BARS   []*BAR  `json:"BARS"`
}

type VERSION struct {
	ID    int `json:"ID"`
	MAJOR int `json:"MAJOR"`
	MINOR int `json:"MINOR"`
}

type SERIAL struct {
	PORT     string `json:"PORT"`
	BAUDRATE int    `json:"BAUDRATE"`
	COMMAND  string `json:"COMMAND"`
}

type BAR struct {
	ID  int   `json:"ID"`
	LCS byte  `json:"LCS"`
	LC  []*LC `json:"LC,omitempty"`
}

type LC struct {
	ZERO   uint64  `json:"ZERO"`
	FACTOR float32 `json:"FACTOR"`
	IEEE   string  `json:"IEEE"`
}

var (
	calibmsg       = "\nPut %d on the %s Bay on the %s side in the %s of the Shelf and Press 'C' to continue. Or <ESC> to exit."
	zeromsg        = "\nClear the Bay(s) and Press 'C' to continue. Or <ESC> to exit."
	lastParameters *PARAMETERS // store parsed parameters for dynamic targets
	immediateRetry bool
)

// App version variables. Set these at build time with -ldflags if desired.
var (
	AppVersion = "dev"
	AppBuild   = "local"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("Usage: calrunrilla <config.json>")
	}

	// Support a simple version flag for CI and quick checks. If any argument is
	// `-v` or `--version` print a plain-text version and exit before any other
	// output so it is always visible and never treated as a config filename.
	for _, a := range os.Args[1:] {
		if a == "--version" || a == "-v" {
			fmt.Printf("%s\n", strings.TrimSpace(fmt.Sprintf("%s [build %s]", AppVersion, AppBuild)))
			return
		}
	}

	// Find the first non-flag argument and treat it as the config path. This
	// prevents flags (like --version) from being interpreted as a filename.
	configPath := ""
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		configPath = a
		break
	}
	if configPath == "" {
		log.Fatal("Usage: calrunrilla <config.json>")
	}

	// Route the standard logger output through our package-scope redWriter
	log.SetFlags(0)
	log.SetOutput(redWriter{os.Stderr})

	// ...existing code...

	// Informational debug line
	debugPrintf(true, "calrunrilla starting with config: %s\n", configPath)

	// ...existing code...

	for {
		clearScreen()
		// Print application banner after clearing the screen so it remains visible
		greenPrintf("Runrilla Calibration version: %s [build %s]\n", AppVersion, AppBuild)
		fmt.Println("--------------------------------------------")
		barsPerRow := calcBarsPerRow(getTerminalWidth())

		calRunrilla(configPath, barsPerRow)
		if immediateRetry {
			// reset and immediately restart loop
			immediateRetry = false
			continue
		}

		// Use the green single-key prompt so 'R' works without Enter
		if !nextRetryOrExit() {
			break
		}
	}
}

func calRunrilla(args0 string, barsPerRow int) {
	jsonData, err := os.ReadFile(args0)
	if err != nil {
		log.Fatalf("Error reading file: %v", err)
	}

	var parameters PARAMETERS
	if err := json.Unmarshal(jsonData, &parameters); err != nil {
		log.Fatalf("JSON error: %v", err)
	}
	// Inform user config loaded (debug-only yellow)
	debugPrintf(parameters.DEBUG, "Loaded config: %s (DEBUG=%v)\n", args0, parameters.DEBUG)

	// Fallback: if IGNORE not provided use AVG
	if parameters.IGNORE <= 0 {
		parameters.IGNORE = parameters.AVG
	}
	lastParameters = &parameters

	if len(parameters.BARS) == 0 {
		log.Fatal("No Bars defined")
	}

	// Ensure we have a working serial port: if PORT missing OR cannot be opened OR version probe fails, auto-detect.
	if parameters.SERIAL == nil {
		log.Fatal("Missing SERIAL section in JSON")
	}
	debugPrintf(parameters.DEBUG, "Validating SERIAL configuration...\n")
	needDetect := false
	if parameters.SERIAL.PORT == "" {
		debugPrintf(parameters.DEBUG, "Serial PORT missing in JSON, attempting auto-detect...\n")
		needDetect = true
	} else {
		// Try opening specified port directly before constructing Leo485 to avoid fatal inside NewLeo485
		debugPrintf(parameters.DEBUG, "Trying configured port: %s (baud %d)\n", parameters.SERIAL.PORT, parameters.SERIAL.BAUDRATE)
		cfg := &serial.Config{Name: parameters.SERIAL.PORT, Baud: parameters.SERIAL.BAUDRATE, Parity: serial.ParityNone, Size: 8, StopBits: serial.Stop1, ReadTimeout: time.Millisecond * 300}
		sp, err := serial.OpenPort(cfg)
		if err != nil {
			log.Printf("Port %s open failed (%v), attempting auto-detect...\n", parameters.SERIAL.PORT, err)
			needDetect = true
		} else {
			sp.Close()
		}
	}
	if needDetect {
		debugPrintf(parameters.DEBUG, "Starting serial auto-detect across COM ports (this may take a few seconds)...\n")
		p := autoDetectPort(&parameters)
		if p == "" {
			log.Fatal("Could not auto-detect serial port")
		}
		parameters.SERIAL.PORT = p
		persistParameters(args0, &parameters)
		debugPrintf(parameters.DEBUG, "Detected serial port: %s (saved to JSON)\n", p)
	}

	debugPrintf(parameters.DEBUG, "Opening Leo485 with port %s...\n", parameters.SERIAL.PORT)
	bars := NewLeo485(parameters.SERIAL, parameters.BARS)
	defer bars.Close()

	// Quick version probe; if fails, try auto-detect fallback (in case wrong but openable port)
	debugPrintf(parameters.DEBUG, "Probing device version...\n")
	if !probeVersion(bars, &parameters) {
		log.Printf("No version response from %s. Attempting reboot of all bars...\n", parameters.SERIAL.PORT)
		// Try to reboot each bar once and allow time to recover
		for i := range bars.Bars {
			if bars.Reboot(i) {
				greenPrintf("Bar %d reboot command sent\n", i+1)
			} else {
				log.Printf("Bar %d reboot command failed or no response\n", i+1)
			}
			time.Sleep(200 * time.Millisecond)
		}
		// Wait a short while for devices to restart
		greenPrintf("Waiting for bars to reboot...\n")
		time.Sleep(1500 * time.Millisecond)
		// Try probing again
		if probeVersion(bars, &parameters) {
			greenPrintf("Version response received after reboot\n")
		} else {
			log.Printf("No version response from %s after reboot, re-attempting auto-detect...\n", parameters.SERIAL.PORT)
			bars.Close()
			p := autoDetectPort(&parameters)
			if p != "" && p != parameters.SERIAL.PORT {
				parameters.SERIAL.PORT = p
				persistParameters(args0, &parameters)
				debugPrintf(parameters.DEBUG, "Updated serial port after probe: %s (saved)\n", p)
				bars = NewLeo485(parameters.SERIAL, parameters.BARS)
				defer bars.Close()
			}
		}
	}

	// Full version validation (will continue even if minor mismatch)
	if !checkVersion(bars, &parameters) {
		// Version check failed but continue
	}

	// Zero Calibration
	debugPrintf(parameters.DEBUG, "Starting zero calibration...\n")
	ad0 := zeroCalibration(bars, &parameters)

	// Weight Calibration
	// blank line between final ZERO output and weight calibration prompt
	fmt.Println()
	debugPrintf(parameters.DEBUG, "Starting weight calibration...\n")
	adv := weightCalibration(bars, &parameters)
	// Empty line between last data line and matrices block
	fmt.Println()
	// Prompt user to clear all bays before computing factors/matrices.
	greenPrintf("Clear all the bays and Press 'C' to continue. Or <ESC> to exit.\n")
	// Wait for single-key 'C' or ESC
	DrainKeys()
	keyEventsPrompt := StartKeyEvents()
	for {
		k := <-keyEventsPrompt
		if k == 27 { // ESC
			log.Fatal("Process cancelled")
		}
		if k == 'C' || k == 'c' {
			break
		}
	}
	// Show matrices only when DEBUG flag is on
	var add *Matrix
	var w *Vector
	if parameters.DEBUG {
		printMatrix(ad0, "Zero Matrix (ad0)")
		printMatrix(adv, "Weight Matrix (adv)")
		add = adv.Sub(ad0)
		printMatrix(add, "Difference Matrix (adv - ad0)")
		w = NewVectorWithValue(adv.Rows, float64(parameters.WEIGHT))
		printVector(w, "Load Vector (W)")
	}

	// Calculate factors
	debug := calcZerosFactors(adv, ad0, &parameters)

	// Add to debug file
	if parameters.DEBUG {
		res := fmt.Sprintf("%s,%s", time.Now().Format("2006-01-02 15:04:05"), debug)
		appendToFile(strings.Replace(args0, ".json", "_debug.csv", 1), res)
	}

	// Single-key Y/N prompt in green. Y will save+flash. N will ask to Restart (R) or Exit (ESC).
	resp := nextYN("Do you want to flash the bars and save the parameters file? (Y/N)")
	switch resp {
	case 'Y':
		saveToJSON(strings.Replace(args0, ".json", "_calibrated.json", 1), &parameters)
		for {
			if err := flashParameters(bars, &parameters); err != nil {
				log.Printf("Flash error: %v", err)
				// Ask user whether to retry flashing, skip, or exit
				a := nextFlashAction()
				if a == 'F' {
					// retry
					continue
				}
				if a == 'S' {
					break // skip flashing
				}
				if a == 27 {
					os.Exit(0)
				}
				break
			} else {
				// success
				break
			}
		}
	case 'N':
		// Show green prompt asking to Retry (R) or Exit (ESC)
		retry := nextRetryOrExit()
		if retry {
			immediateRetry = true
			return
		}
		// Otherwise exit
		os.Exit(0)
	case 27: // ESC
		os.Exit(0)
	}
}

// nextYN shows a green prompt and waits for single-key Y/N (case-insensitive). If N is pressed
// it returns 'N' and the caller can choose to restart or exit. If ESC pressed, returns 27.
func nextYN(message string) rune {
	// Print message in green
	fmt.Printf("\033[32m%s\033[0m\n", message)
	DrainKeys()
	keyEvents := StartKeyEvents()
	for {
		k := <-keyEvents
		if k == 'Y' || k == 'y' {
			return 'Y'
		}
		if k == 'N' || k == 'n' {
			return 'N'
		}
		if k == 27 { // ESC
			return 27
		}
		if k == 'R' || k == 'r' {
			// Treat as restart choice
			return 'N'
		}
	}
}

// nextRetryOrExit shows a green message and waits for a single 'R' (restart) or ESC (exit).
// Returns true if user chose restart, false to exit.
func nextRetryOrExit() bool {
	msg := "\nPress 'R' to Retry, <ESC> to exit"
	fmt.Printf("\033[32m%s\033[0m\n", msg)
	DrainKeys()
	keyEvents := StartKeyEvents()
	for {
		k := <-keyEvents
		if k == 'R' || k == 'r' {
			return true
		}
		if k == 27 { // ESC
			return false
		}
	}
}

// nextFlashAction prompts the user after a flash failure: F to retry flash, S to skip, ESC to exit.
func nextFlashAction() rune {
	msg := "\nFlash failed. Press 'F' to retry, 'S' to skip flashing, or <ESC> to exit"
	fmt.Printf("\033[33m%s\033[0m\n", msg)
	DrainKeys()
	keyEvents := StartKeyEvents()
	for {
		k := <-keyEvents
		if k == 'F' || k == 'f' {
			return 'F'
		}
		if k == 'S' || k == 's' {
			return 'S'
		}
		if k == 27 {
			return 27
		}
	}
}

func next(message string, nextKey rune) bool {
	fmt.Println(message)
	reader := bufio.NewReader(os.Stdin)
	for {
		char, _, err := reader.ReadRune()
		if err != nil {
			return false
		}
		if char == nextKey {
			return true
		}
		if char == 27 { // ESC
			return false
		}
	}
}

func zeroCalibration(bars *Leo485, parameters *PARAMETERS) *Matrix {
	ads, ok := showADCLabel(bars, zeromsg, "[ZERO]")
	if !ok {
		log.Fatal("Process cancelled")
	}
	// Empty line between final data and next phase instructions
	fmt.Println()
	return updateMatrixZero(ads, 3*(len(parameters.BARS)-1), bars.NLCs)
}

func weightCalibration(bars *Leo485, parameters *PARAMETERS) *Matrix {
	nlcs := bars.NLCs
	nbars := len(parameters.BARS)
	nloads := 3 * (nbars - 1) * nlcs
	nbars *= nlcs
	adv := NewMatrix(nloads, nbars)

	for j := 0; j < nloads; j++ {
		adv = weightCalibrationSingle(bars, parameters, adv, j)
	}
	return adv
}

func weightCalibrationSingle(bars *Leo485, parameters *PARAMETERS, adv *Matrix, index int) *Matrix {
	sb := fmt.Sprintf(calibmsg, parameters.WEIGHT, (BAY)(index/6), (LMR)((index/2)%3), (FB)(index%2))
	// Label as running index (left side): [0001], [0002], ...
	lbl := fmt.Sprintf("[%04d]", index+1)
	ads, ok := showADCLabel(bars, sb, lbl)
	if !ok {
		log.Fatal("Process cancelled")
	}
	// Empty line between final data and next phase instructions
	fmt.Println()
	return updateMatrixUpdate(adv, ads, index, bars.NLCs)
}

// indexTitle unused; kept for reference

func updateMatrixZero(ads []int64, calibs, nlcs int) *Matrix {
	ad := NewVector(len(ads))
	for i, v := range ads {
		ad.Values[i] = float64(v)
	}

	// Suppress extra right-side marker in batch output
	nbars := len(ads) / nlcs
	ad0 := NewMatrix(calibs*nlcs, nbars*nlcs)
	for i := 0; i < calibs*nlcs; i++ {
		ad0.SetRow(i, ad)
	}
	return ad0
}

func updateMatrixUpdate(adc *Matrix, ads []int64, index, nlcs int) *Matrix {
	// Suppress extra right-side stage number; left side shows it via interactive label
	nbars := len(ads) / nlcs
	for j := 0; j < nbars; j++ {
		for i := 0; i < nlcs; i++ {
			curr := j*nlcs + i
			adc.Values[index][curr] = float64(ads[curr])
		}
	}
	return adc
}

func showADCLabel(bars *Leo485, message string, finalLabel string) ([]int64, bool) {
	// Green instruction line
	fmt.Printf("\033[32m%s\033[0m\n", message)
	return manipulateADC(bars, finalLabel)
}

func showADC(bars *Leo485, message string) ([]int64, bool) {
	// default label when unspecified
	return showADCLabel(bars, message, "[ZERO]")
}

func manipulateADC(bars *Leo485, finalLabel string) ([]int64, bool) {
	// Print instruction once
	fmt.Println()
	// Clear any pending key presses from previous phase to avoid accidental triggers
	DrainKeys()

	// Phase management
	phase := "live" // "live", "ignoring", "averaging", "finished"
	ignoreCounter := 0
	avgCounter := 0
	// Dynamic targets from JSON (parameters stored globally via lastParameters)
	ignoreTarget := 50
	avgTarget := 100
	if lastParameters != nil {
		if lastParameters.IGNORE > 0 {
			ignoreTarget = lastParameters.IGNORE
		}
		if lastParameters.AVG > 0 {
			avgTarget = lastParameters.AVG
		}
	}

	// Variables for averaging
	samples := make([][][]int64, len(bars.Bars))
	for i := range samples {
		samples[i] = make([][]int64, 0)
	}

	var finalAverages [][]int64

	keyEvents := StartKeyEvents() // raw mode channel (no Enter)

	for {
		// Check for keyboard input - only in live phase
		if phase == "live" {
			select {
			case k := <-keyEvents:
				if k == 27 { // ESC
					return nil, false
				}
				if k == 'C' || k == 'c' {
					phase = "ignoring"
					ignoreCounter = 0
				}
			default:
			}
		} // Get current readings
		currentSample := make([][]int64, len(bars.Bars))
		for i := range bars.Bars {
			bruts, err := bars.GetADs(i)
			if err == nil && len(bruts) > 0 {
				// capture all load cells for proper matrix population
				full := make([]int64, len(bruts))
				for k, v := range bruts {
					full[k] = int64(v)
				}
				currentSample[i] = full
			} else {
				currentSample[i] = make([]int64, bars.NLCs)
			}
		}

		// Process based on phase
		switch phase {
		case "live":
			printLiveLine(bars, currentSample)
		case "ignoring":
			ignoreCounter++
			printIgnoringLine(bars, currentSample, ignoreCounter, ignoreTarget)
			if ignoreCounter >= ignoreTarget {
				phase = "averaging"
				avgCounter = 0
				// Clear samples for fresh start
				for i := range samples {
					samples[i] = make([][]int64, 0)
				}
			}
		case "averaging":
			avgCounter++
			// Collect samples for averaging
			for i := range bars.Bars {
				samples[i] = append(samples[i], currentSample[i])
			}
			printAveragingLine(bars, currentSample, avgCounter, avgTarget)
			if avgCounter >= avgTarget {
				phase = "finished"
				finalAverages = calculateFinalAverages(samples, bars.NLCs)
			}
		case "finished":
			// Show final averages once, then automatically advance (no key required)
			printFinalLine(bars, finalAverages, finalLabel)
			// Flatten final averages to []int64 for downstream use
			flat := make([]int64, len(bars.Bars)*bars.NLCs)
			for i := range bars.Bars {
				if i < len(finalAverages) {
					for lc := 0; lc < bars.NLCs && lc < len(finalAverages[i]); lc++ {
						flat[i*bars.NLCs+lc] = finalAverages[i][lc]
					}
				}
			}
			return flat, true
		}

		// Small sleep to prevent excessive CPU usage
		time.Sleep(5 * time.Millisecond)
	}
}

func printLiveLine(bars *Leo485, currentSample [][]int64) {
	line := "\r[LIVE] "
	for i := range bars.Bars {
		if i < len(currentSample) && len(currentSample[i]) >= 2 {
			line += fmt.Sprintf("(%02d):%010d/%010d  ", i+1, currentSample[i][0], currentSample[i][1])
		}
	}
	line += "                    "
	fmt.Print(line)
}

func printIgnoringLine(bars *Leo485, currentSample [][]int64, counter, target int) {
	// Light purple entire line (live ignoring phase inside interactive calibration)
	line := fmt.Sprintf("\r\033[95m[IGN %04d] ", counter)
	for i := range bars.Bars {
		if i < len(currentSample) && len(currentSample[i]) >= 2 {
			line += fmt.Sprintf("(%02d):%010d/%010d  ", i+1, currentSample[i][0], currentSample[i][1])
		}
	}
	line += "                    \033[0m"
	fmt.Print(line)
}

func printAveragingLine(bars *Leo485, currentSample [][]int64, counter, target int) {
	// Light blue entire line (averaging phase inside interactive calibration)
	line := fmt.Sprintf("\r\033[96m[AVG %04d] ", counter)
	for i := range bars.Bars {
		if i < len(currentSample) && len(currentSample[i]) >= 2 {
			line += fmt.Sprintf("(%02d):%010d/%010d  ", i+1, currentSample[i][0], currentSample[i][1])
		}
	}
	line += "                    \033[0m"
	fmt.Print(line)
}

func printFinalLine(bars *Leo485, finalAverages [][]int64, label string) {
	// Dark blue entire line with provided label
	line := "\r\033[34m" + label + " "
	for i := range bars.Bars {
		if i < len(finalAverages) && len(finalAverages[i]) >= 2 {
			line += fmt.Sprintf("(%02d):%010d/%010d  ", i+1, finalAverages[i][0], finalAverages[i][1])
		}
	}
	line += "                    \033[0m"
	fmt.Print(line)
}

func calculateFinalAverages(samples [][][]int64, nlcs int) [][]int64 {
	finalAverages := make([][]int64, len(samples))
	for i, barSamples := range samples {
		if len(barSamples) == 0 {
			finalAverages[i] = make([]int64, nlcs)
			continue
		}
		counts := make([]int64, nlcs)
		sums := make([]int64, nlcs)
		for _, sample := range barSamples {
			for lc := 0; lc < nlcs && lc < len(sample); lc++ {
				sums[lc] += sample[lc]
				counts[lc]++
			}
		}
		avg := make([]int64, nlcs)
		for lc := 0; lc < nlcs; lc++ {
			if counts[lc] > 0 {
				avg[lc] = sums[lc] / counts[lc]
			}
		}
		finalAverages[i] = avg
	}
	return finalAverages
}

// printMatrix dumps the full matrix (may be large). For debugging only.
func printMatrix(m *Matrix, title string) {
	// Yellow for debug matrices
	if lastParameters != nil && lastParameters.DEBUG {
		fmt.Print("\033[33m")
	}
	fmt.Println(matrixline)
	fmt.Println(title, " (", m.Rows, "x", m.Cols, ")")
	maxRows := m.Rows
	if maxRows > 12 { // limit output for readability
		maxRows = 12
	}
	for i := 0; i < maxRows; i++ {
		row := m.Values[i]
		line := fmt.Sprintf("[%03d]", i)
		maxCols := len(row)
		if maxCols > 16 {
			maxCols = 16
		}
		for j := 0; j < maxCols; j++ {
			line += fmt.Sprintf(" %10.0f", row[j])
		}
		if len(row) > maxCols {
			line += " ..."
		}
		fmt.Println(line)
	}
	if m.Rows > maxRows {
		fmt.Println("...")
	}
	fmt.Println(matrixline)
	if lastParameters != nil && lastParameters.DEBUG {
		fmt.Print("\033[0m")
	}
}

// printVector dumps a trimmed view of a vector for debugging
func printVector(v *Vector, title string) {
	if lastParameters != nil && lastParameters.DEBUG {
		fmt.Print("\033[33m")
	}
	fmt.Println(matrixline)
	fmt.Println(title, " (", v.Length, ")")
	max := v.Length
	if max > 24 {
		max = 24
	}
	for i := 0; i < max; i++ {
		fmt.Printf("[%03d] %10.0f\n", i, v.Values[i])
	}
	if v.Length > max {
		fmt.Println("...")
	}
	fmt.Println(matrixline)
	if lastParameters != nil && lastParameters.DEBUG {
		fmt.Print("\033[0m")
	}
}

func printAveragedLine(bars *Leo485, samples [][][]int64) {
	// Build complete line with averaged data
	line := "\r[Live-AVG] "

	for i := range bars.Bars {
		if len(samples[i]) > 0 {
			// Calculate averages for this bar
			var sum1, sum2 int64
			count := len(samples[i])

			for _, sample := range samples[i] {
				if len(sample) >= 2 {
					sum1 += sample[0]
					sum2 += sample[1]
				}
			}

			avg1 := sum1 / int64(count)
			avg2 := sum2 / int64(count)

			line += fmt.Sprintf("(%02d):%010d/%010d  ", i+1, avg1, avg2)
		} else {
			line += fmt.Sprintf("(%02d):0000000000/0000000000  ", i+1)
		}
	}

	// Add padding to clear any leftover characters
	line += "                    "

	fmt.Print(line)
}

func printSingleLine(bars *Leo485) {
	// Build complete line with all data
	line := "\r[Live] "

	for i := range bars.Bars {
		bruts, err := bars.GetADs(i)
		val1 := int64(0)
		val2 := int64(0)

		if err == nil {
			if len(bruts) > 0 {
				val1 = int64(bruts[0])
			}
			if len(bruts) > 1 {
				val2 = int64(bruts[1])
			}
		}

		line += fmt.Sprintf("(%02d):%010d/%010d  ", i+1, val1, val2)
	}

	// Add padding to clear any leftover characters
	line += "                    "

	fmt.Print(line)
}

func getCurrentValues(bars *Leo485) [][]int64 {
	values := make([][]int64, len(bars.Bars))
	for i := range bars.Bars {
		bruts, err := bars.GetADs(i)
		if err == nil && len(bruts) >= 2 {
			values[i] = []int64{int64(bruts[0]), int64(bruts[1])}
		} else {
			values[i] = []int64{0, 0}
		}
	}
	return values
}

func clearAndShowData(bars *Leo485) {
	// Clear screen using Windows cls command
	fmt.Print("\033[2J\033[H")

	// Print header
	fmt.Println("Clear the Bay(s) and Press 'C' to continue. Or <ESC> to exit.")
	fmt.Println()

	// Print live data in two-line format
	// First line
	fmt.Printf("[Live]")
	for i := range bars.Bars {
		bruts, err := bars.GetADs(i)
		val1 := int64(0)
		if err == nil && len(bruts) > 0 {
			val1 = int64(bruts[0])
		}
		fmt.Printf("(%02d):[1]%010d   ", i+1, val1)
	}
	fmt.Println()

	// Second line
	fmt.Printf("           ")
	for i := range bars.Bars {
		bruts, err := bars.GetADs(i)
		val2 := int64(0)
		if err == nil && len(bruts) > 1 {
			val2 = int64(bruts[1])
		}
		fmt.Printf("[2]%010d        ", val2)
	}
	fmt.Println()
}

func calcZerosFactors(adv, ad0 *Matrix, parameters *PARAMETERS) string {
	debug := "\n"
	add := adv.Sub(ad0)
	w := NewVectorWithValue(adv.Rows, float64(parameters.WEIGHT))
	adi := add.InverseSVD()
	if adi == nil {
		log.Fatal("SVD failed; cannot compute pseudoinverse")
	}

	// Solve f = A^+ * W
	factors := adi.MulVector(w)
	if factors == nil {
		log.Fatal("pseudoinverse multiplication failed")
	}

	// Zeros are first row of ad0
	zeros := ad0.GetRow(0)
	recordData(debug, zeros, "Zeros", "%10.0f")
	// Print only IEEE754-formatted factors block (no separate decimal-only list)
	printFactorsIEEE(factors)

	if parameters.DEBUG {
		// Yellow color for debug diagnostics block
		fmt.Print("\033[33m")
		check := add.MulVector(factors)
		// Show check with only one digit after the decimal point
		recordData(debug, check, "Check", "%8.1f")
		fmt.Println(matrixline)
		norm := check.Sub(w).Norm() / float64(parameters.WEIGHT)
		// Print diagnostics in yellow (debug-only)
		fmt.Print("\033[33m")
		fmt.Printf("Error: %e\n", norm)
		debug += fmt.Sprintf("Error,%e\n", norm)
		fmt.Println(matrixline)

		fmt.Printf("Pseudoinverse Norm: %e\n", adi.Norm())
		debug += fmt.Sprintf("PseudoinverseNorm,%e\n", adi.Norm())
		fmt.Println(matrixline)
		fmt.Print("\033[0m")
		// Reset color after debug block
		fmt.Print("\033[0m")
		debug += matrixline + "\n"
	}

	nbars := len(parameters.BARS)
	nlcs := zeros.Length / nbars

	for i := 0; i < nbars; i++ {
		parameters.BARS[i].LC = make([]*LC, nlcs)
		for j := 0; j < nlcs; j++ {
			index := i*nlcs + j
			lc := &LC{
				ZERO:   uint64(zeros.Values[index]),
				FACTOR: float32(factors.Values[index]),
				IEEE:   fmt.Sprintf("%08X", ToIEEE754(float32(factors.Values[index]))),
			}
			parameters.BARS[i].LC[j] = lc
		}
	}
	return debug
}

func recordData(debug string, vec *Vector, title, format string) string {
	text, csv := vec.ToStrings(title, format)
	// Orange (approx) for zeros and factors always
	if title == "Zeros" || title == "factors" {
		fmt.Print("\033[38;5;208m")
		fmt.Println(text)
		fmt.Print("\033[0m")
	} else {
		fmt.Println(text)
	}
	return debug + csv + "\n"
}

// printFactorsIEEE prints the factors as IEEE754 hex with decimal values, matching requested formatting
func printFactorsIEEE(factors *Vector) {
	// Orange color for factors
	fmt.Print("\033[38;5;208m")
	fmt.Println(matrixline)
	fmt.Println("factors (IEEE754)")
	for i, val := range factors.Values {
		hex := fmt.Sprintf("%08X", ToIEEE754(float32(val)))
		// Use space flag to align sign: positive numbers get a leading space, negatives show '-'
		// This keeps the decimal column aligned regardless of sign.
		fmt.Printf("[%03d]  % .12f  %s\n", i, val, hex)
	}
	fmt.Println(matrixline)
	fmt.Print("\033[0m")
}

func saveToJSON(file string, parameters *PARAMETERS) {
	sentinel := &SENTINEL{
		SERIAL: parameters.SERIAL,
		BARS:   parameters.BARS,
	}
	data, _ := json.MarshalIndent(sentinel, "", "  ")
	os.WriteFile(file, data, 0644)
	greenPrintf("%s Saved\n", file)

	// Also write a small adjacent version file so the app version is recorded
	// without altering the parameters JSON schema.
	verFile := strings.TrimSuffix(file, ".json") + ".version"
	// Write version file as two tokens so CI/builds can inject numeric values
	verContent := fmt.Sprintf("%s %s\n", AppVersion, AppBuild)
	_ = os.WriteFile(verFile, []byte(verContent), 0644)
}

func appendToFile(file, content string) {
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(content + "\n")
}

func flashParameters(bars *Leo485, parameters *PARAMETERS) error {
	if len(parameters.BARS) == 0 || len(parameters.BARS[0].LC) == 0 {
		return nil
	}
	if err := bars.OpenToUpdate(); err != nil {
		// Try one recovery step: reboot all bars and wait briefly, then retry OpenToUpdate once.
		log.Printf("OpenToUpdate failed: %v. Attempting reboot of all bars and retrying...", err)
		for i := range bars.Bars {
			bars.Reboot(i)
			time.Sleep(100 * time.Millisecond)
		}
		time.Sleep(1500 * time.Millisecond)
		if err2 := bars.OpenToUpdate(); err2 != nil {
			return fmt.Errorf("cannot enter update mode: %v; retry: %v", err, err2)
		}
	}

	// At this point we sent the Euler sequence once. Some bars may respond with
	// "Enter" asynchronously. Wait until all bars report the Enter prompt before
	// proceeding to flash data. This prevents the earlier-first-attempt-fail behavior
	// where one bar responds later than others.
	notReady := make([]int, 0)
	for i := 0; i < len(parameters.BARS); i++ {
		notReady = append(notReady, i)
	}
	// Retry loop: try up to 6 times (about ~3s total) to collect Enter from all bars.
	for attempt := 1; attempt <= 6 && len(notReady) > 0; attempt++ {
		remaining := make([]int, 0)
		for _, idx := range notReady {
			cmd := GetCommand(parameters.BARS[idx].ID, []byte(Euler))
			resp, err := changeState(bars.Serial, cmd, 400)
			if err != nil {
				if parameters.DEBUG {
					debugPrintf(true, "Euler handshake bar %d attempt %d err=%v resp=%q\n", idx+1, attempt, err, resp)
				}
				remaining = append(remaining, idx)
				continue
			}
			if !strings.Contains(resp, "Enter") {
				if parameters.DEBUG {
					debugPrintf(true, "Euler handshake bar %d attempt %d no Enter resp=%q\n", idx+1, attempt, resp)
				}
				remaining = append(remaining, idx)
				continue
			}
			// Got Enter for this bar
			if parameters.DEBUG {
				debugPrintf(true, "Euler handshake bar %d attempt %d OK resp=%q\n", idx+1, attempt, resp)
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

	// All bars reported Enter; some devices ignore the very first command.
	// Send a dummy CR to each bar to prime the bootloader, then wait briefly.
	if parameters.DEBUG {
		debugPrintf(true, "All bars entered update mode; sending dummy CR to bays\n")
	}
	// send a single CR once to prime all bootloaders
	_, _ = bars.Serial.Write([]byte{0x0D})
	// small read to clear any immediate reply (use lower-level readUntil)
	_, _ = readUntil(bars.Serial, 50)

	nbars := len(parameters.BARS)
	for i := 0; i < nbars; i++ {
		greenPrintf("\nBAR(%02d)\n", i+1)
		greenPrintf(" ID=%d\n", parameters.BARS[i].ID)
		lcs := activeLCs(parameters.BARS[i])
		greenPrintf(" LCS=%d\n", lcs)

		nlcs := len(parameters.BARS[i].LC)
		zero := NewVector(nlcs)
		facs := NewVector(nlcs)
		zeravg := 0.0
		for j := 0; j < nlcs; j++ {
			zero.Values[j] = float64(parameters.BARS[i].LC[j].ZERO)
			facs.Values[j] = float64(parameters.BARS[i].LC[j].FACTOR)
			zeravg += zero.Values[j] * facs.Values[j]
		}
		if zeravg < 0 {
			zeravg = 0
			fmt.Println("Avg. Zero reference is negative")
		}
		greenPrintf(" Flashing Zeros:\n")
		// Attempt to write zeros with retries and debug logging
		// Build the O command payload same as WriteZeros expects
		sb := "O"
		k := 0
		for ii := 0; ii < 4; ii++ {
			if (parameters.BARS[i].LCS & (1 << ii)) != 0 {
				sb += fmt.Sprintf("%09.0f|", zero.Values[k])
				k++
			} else {
				sb += fmt.Sprintf("%09d|", 0)
			}
		}
		sb += fmt.Sprintf("%09d|", uint64(zeravg/float64(nlcs)+0.5))
		zeroCmd := GetCommand(parameters.BARS[i].ID, []byte(sb))
		wroteZeros := false
		for attempt := 1; attempt <= 3; attempt++ {
			resp, err := updateValue(bars.Serial, zeroCmd, 200)
			if err == nil && strings.Contains(resp, "OK") {
				wroteZeros = true
				if parameters.DEBUG {
					debugPrintf(true, "WriteZeros ok (attempt %d): %s\n", attempt, resp)
				}
				break
			}
			if parameters.DEBUG {
				debugPrintf(true, "WriteZeros attempt %d failed: err=%v resp=%q\n", attempt, err, resp)
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !wroteZeros {
			fmt.Println(" Cannot flash Zeros to Bar")
			continue
		}

		greenPrintf(" Flashing factors:\n")
		// Build X command payload
		sb2 := "X"
		k2 := 0
		for ii := 0; ii < 4; ii++ {
			if (parameters.BARS[i].LCS & (1 << ii)) != 0 {
				sb2 += fmt.Sprintf("%.10f|", facs.Values[k2])
				k2++
			} else {
				sb2 += "1.0000000000|"
			}
		}
		facCmd := GetCommand(parameters.BARS[i].ID, []byte(sb2))
		wroteFacs := false
		for attempt := 1; attempt <= 3; attempt++ {
			resp, err := updateValue(bars.Serial, facCmd, 200)
			if err == nil && strings.Contains(resp, "OK") {
				wroteFacs = true
				if parameters.DEBUG {
					debugPrintf(true, "WriteFactors ok (attempt %d): %s\n", attempt, resp)
				}
				break
			}
			if parameters.DEBUG {
				debugPrintf(true, "WriteFactors attempt %d failed: err=%v resp=%q\n", attempt, err, resp)
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !wroteFacs {
			fmt.Println(" Cannot flash Factors to Bar")
			continue
		}

		if bars.Reboot(i) {
			debugPrintf(parameters.DEBUG, "Bar %d reboot command sent\n", i+1)
		} else {
			log.Printf("Bar %d reboot command failed or no response\n", i+1)
		}
		greenPrintf(" Flashed!\n")
	}
	return nil
}

func checkVersion(bars *Leo485, parameters *PARAMETERS) bool {
	// Expected values (can be made configurable later)
	expectedID := 12009
	expectedMajor := 1
	expectedMinor := 202

	var firstID, firstMajor, firstMinor int
	anyError := false

	for i := range bars.Bars {
		id, major, minor, e := bars.GetVersion(i)
		if e != nil {
			log.Printf("Bar %d: version probe error: %v", i+1, e)
			anyError = true
			continue
		}

		// Print discovered version info with coloring for clarity
		if id != expectedID {
			log.Printf("\033[31mBar %d: Unexpected Version ID %d (expected %d)\033[0m", i+1, id, expectedID)
			anyError = true
		} else if major != expectedMajor {
			log.Printf("\033[33mBar %d: Version major %d (expected %d)\033[0m", i+1, major, expectedMajor)
			// non-fatal
		} else if minor != expectedMinor {
			log.Printf("\033[33mBar %d: Version minor %d (expected %d)\033[0m", i+1, minor, expectedMinor)
			// non-fatal
		} else {
			greenPrintf("Bar %d: Version OK (ID=%d %d.%d)\n", i+1, id, major, minor)
		}

		if firstID == 0 {
			firstID = id
			firstMajor = major
			firstMinor = minor
		}

		time.Sleep(200 * time.Millisecond)
	}

	// Store discovered version in parameters if available
	if firstID != 0 {
		if parameters.VERSION == nil {
			parameters.VERSION = &VERSION{}
		}
		parameters.VERSION.ID = firstID
		parameters.VERSION.MAJOR = firstMajor
		parameters.VERSION.MINOR = firstMinor
	}

	return !anyError
}

func activeLCs(bar *BAR) int {
	n := 0
	for i := 0; i < MAXLCS; i++ {
		if (bar.LCS & (1 << i)) != 0 {
			n = n*10 + (i + 1)
		}
	}
	return n
}

func calcBarsPerRow(width int) int {
	if width <= 0 {
		return 1
	}
	e := float64(width) / 22.8
	bars := int(e - 0.45)
	if bars < 1 {
		bars = 1
	}
	return bars
}

func getTerminalWidth() int {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return 80
	}
	parts := strings.Fields(string(out))
	if len(parts) < 2 {
		return 80
	}
	w, _ := strconv.Atoi(parts[1])
	return w
}

func clearScreen() {
	fmt.Print("\033[2J\033[1;1H")
}

// eulerTest sends the Euler maintenance sequence to every bar and prints
// the raw response (length, hex dump, trimmed string) to help diagnose
// whether the device enters maintenance mode and replies with "Enter".
// ...existing code...

// autoDetectPort scans common COM ports to find one responding to a Version command.
func autoDetectPort(parameters *PARAMETERS) string {
	expectedFirstBarID := parameters.BARS[0].ID
	baud := parameters.SERIAL.BAUDRATE
	// Scan COM1..COM64
	for i := 1; i <= 64; i++ {
		portName := fmt.Sprintf("COM%d", i)
		if testPort(portName, expectedFirstBarID, baud) {
			return portName
		}
	}
	return ""
}

// testPort tries to open port and issue a version command to first bar ID.
func testPort(name string, barID int, baud int) bool {
	config := &serial.Config{Name: name, Baud: baud, Parity: serial.ParityNone, Size: 8, StopBits: serial.Stop1, ReadTimeout: time.Millisecond * 300}
	sp, err := serial.OpenPort(config)
	if err != nil {
		return false
	}
	defer sp.Close()
	cmd := GetCommand(barID, []byte("V"))
	resp, err := getData(sp, cmd, 200)
	if err != nil {
		return false
	}
	return strings.Contains(resp, "Version")
}

// probeVersion quickly checks if current bars setup responds to a version query.
func probeVersion(bars *Leo485, parameters *PARAMETERS) bool {
	_, _, _, err := bars.GetVersion(0)
	return err == nil
}

// persistParameters overwrites original JSON with updated parameters (including detected port)
func persistParameters(path string, parameters *PARAMETERS) {
	data, err := json.MarshalIndent(parameters, "", "  ")
	if err != nil {
		fmt.Println("Cannot marshal parameters:", err)
		return
	}
	if writeErr := os.WriteFile(path, data, 0644); writeErr != nil {
		fmt.Println("Cannot write parameters file:", writeErr)
	}
}
