package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	matrix "github.com/CK6170/Calrunrilla-go/matrix"
	serialpkg "github.com/CK6170/Calrunrilla-go/serial"
	ui "github.com/CK6170/Calrunrilla-go/ui"
	"github.com/tarm/serial"
)

// runCalibration performs the full calibration process
func runCalibration(configPath string) {
	jsonData, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("Error reading file: %v", err)
	}

	var parameters PARAMETERS
	if err := json.Unmarshal(jsonData, &parameters); err != nil {
		log.Fatalf("JSON error: %v", err)
	}
	// Inform user config loaded (debug-only yellow)
	ui.Debugf(parameters.DEBUG, "Loaded config: %s (DEBUG=%v)\n", configPath, parameters.DEBUG)

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
	ui.Debugf(parameters.DEBUG, "Validating SERIAL configuration...\n")
	needDetect := false
	if parameters.SERIAL.PORT == "" {
		ui.Debugf(parameters.DEBUG, "Serial PORT missing in JSON, attempting auto-detect...\n")
		needDetect = true
	} else {
		// Try opening specified port directly before constructing Leo485 to avoid fatal inside NewLeo485
		ui.Debugf(parameters.DEBUG, "Trying configured port: %s (baud %d)\n", parameters.SERIAL.PORT, parameters.SERIAL.BAUDRATE)
		cfg := &serial.Config{Name: parameters.SERIAL.PORT, Baud: parameters.SERIAL.BAUDRATE, Parity: serial.ParityNone, Size: 8, StopBits: serial.Stop1, ReadTimeout: time.Millisecond * 300}
		sp, err := serial.OpenPort(cfg)
		if err != nil {
			log.Printf("Port %s open failed (%v), attempting auto-detect...\n", parameters.SERIAL.PORT, err)
			needDetect = true
		} else {
			_ = sp.Close()
		}
	}
	if needDetect {
		ui.Debugf(parameters.DEBUG, "Starting serial auto-detect across COM ports (this may take a few seconds)...\n")
		p := serialpkg.AutoDetectPort(&parameters)
		if p == "" {
			log.Fatal("Could not auto-detect serial port")
		}
		parameters.SERIAL.PORT = p
		persistParameters(configPath, &parameters)
		ui.Debugf(parameters.DEBUG, "Detected serial port: %s (saved to JSON)\n", p)
	}

	ui.Debugf(parameters.DEBUG, "Opening Leo485 with port %s...\n", parameters.SERIAL.PORT)
	bars := serialpkg.NewLeo485(parameters.SERIAL, parameters.BARS)
	defer func() { _ = bars.Close() }()

	// Quick version probe; if fails, try auto-detect fallback (in case wrong but openable port)
	ui.Debugf(parameters.DEBUG, "Probing device version...\n")
	if !probeVersion(bars, &parameters) {
		log.Printf("No version response from %s. Attempting reboot of all bars...\n", parameters.SERIAL.PORT)
		// Try to reboot each bar once and allow time to recover
		for i := range bars.Bars {
			if bars.Reboot(i) {
				ui.Greenf("Bar %d reboot command sent\n", i+1)
			} else {
				log.Printf("Bar %d reboot command failed or no response\n", i+1)
			}
			time.Sleep(200 * time.Millisecond)
		}
		// Wait a short while for devices to restart
		ui.Greenf("Waiting for bars to reboot...\n")
		time.Sleep(1500 * time.Millisecond)
		// Try probing again
		if probeVersion(bars, &parameters) {
			ui.Greenf("Version response received after reboot\n")
		} else {
			log.Printf("No version response from %s after reboot, re-attempting auto-detect...\n", parameters.SERIAL.PORT)
			_ = bars.Close()
			p := serialpkg.AutoDetectPort(&parameters)
			if p != "" && p != parameters.SERIAL.PORT {
				parameters.SERIAL.PORT = p
				persistParameters(configPath, &parameters)
				ui.Debugf(parameters.DEBUG, "Updated serial port after probe: %s (saved)\n", p)
				bars = serialpkg.NewLeo485(parameters.SERIAL, parameters.BARS)
				defer func() { _ = bars.Close() }()
			}
		}
	}

	// Full version validation (will continue even if minor mismatch)
	if !checkVersion(bars, &parameters) {
		// Version check failed but continue
		ui.Warningf("Warning: version check failed, continuing anyway\n")
	}

	// Zero Calibration
	ui.Debugf(parameters.DEBUG, "Starting zero calibration...\n")
	ad0 := zeroCalibration(bars, &parameters)

	// Weight Calibration
	// blank line between final ZERO output and weight calibration prompt
	fmt.Println()
	ui.Debugf(parameters.DEBUG, "Starting weight calibration...\n")
	adv := weightCalibration(bars, &parameters)
	// Empty line between last data line and matrices block
	fmt.Println()
	// Prompt user to clear all bays before computing factors/matrices.
	ui.Greenf("Clear all the bays and Press 'C' to continue. Or <ESC> to exit.\n")
	// Wait for single-key 'C' or ESC
	ui.DrainKeys()
	keyEventsPrompt := ui.StartKeyEvents()
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
	var add *matrix.Matrix
	var w *matrix.Vector
	if parameters.DEBUG {
		printMatrix(ad0, "Zero Matrix (ad0)")
		printMatrix(adv, "Weight Matrix (adv)")
		add = adv.Sub(ad0)
		printMatrix(add, "Difference Matrix (adv - ad0)")
		w = matrix.NewVectorWithValue(adv.Rows, float64(parameters.WEIGHT))
		printVector(w, "Load Vector (W)")

		// Print zeros taken directly from ad0 (no averaging) between Load Vector and Check
		zerosVec := ad0.GetRow(0)
		fmt.Print("\033[38;5;208m")
		fmt.Println(matrix.MatrixLine)
		// Print zeros grouped by Bar (Bar 1 zeros, Bar 2 zeros, ...). Use bars.NLCs because
		// parameters.BARS[].LC isn't populated until after calcZerosFactors.
		idx := 0
		nlcsPerBar := bars.NLCs
		for bi := 0; bi < len(parameters.BARS); bi++ {
			fmt.Printf("Bar %d zeros:\n", bi+1)
			for j := 0; j < nlcsPerBar; j++ {
				fmt.Printf("[%03d]  %12.0f\n", j, zerosVec.Values[idx])
				idx++
			}
			fmt.Println(matrix.MatrixLine)
		}
		fmt.Print("\033[0m")
	}

	// Calculate factors
	debug, factorsVec, adiNorm := calcZerosFactors(adv, ad0, &parameters)

	// Also print per-bar factors (same style as test mode) so operator can review before flashing
	nbars := len(parameters.BARS)
	if nbars > 0 {
		fmt.Print("\033[38;5;208m")
		for i := 0; i < nbars; i++ {
			nlcs := len(parameters.BARS[i].LC)
			fmt.Println(matrix.MatrixLine)
			fmt.Printf("Bar %d factors:\n", i+1)
			for j := 0; j < nlcs; j++ {
				f := float32(parameters.BARS[i].LC[j].FACTOR)
				hex := fmt.Sprintf("%08X", matrix.ToIEEE754(f))
				// match test-mode decimal precision
				fmt.Printf("[%03d]   % .12f  %s\n", j, float64(f), hex)
			}
			fmt.Println(matrix.MatrixLine)
			fmt.Println()
		}
		// Reset color after printing per-bar factors (zeros from ad0 are shown earlier)
		fmt.Print("\033[0m")
	}

	// If DEBUG, print the Check block (re-using the check computed from add * factors)
	if parameters.DEBUG {
		// Ensure we have 'add' and 'w' to perform the check
		add := adv.Sub(ad0)
		w := matrix.NewVectorWithValue(adv.Rows, float64(parameters.WEIGHT))
		check := add.MulVector(factorsVec)
		// Yellow color for the diagnostic Check block
		fmt.Print("\033[33m")
		debug = recordData(debug, check, "Check", "%8.1f")
		fmt.Println(matrix.MatrixLine)
		norm := check.Sub(w).Norm() / float64(parameters.WEIGHT)
		fmt.Printf("Error: %e\n", norm)
		debug += fmt.Sprintf("Error,%e\n", norm)
		fmt.Println(matrix.MatrixLine)

		fmt.Printf("Pseudoinverse Norm: %e\n", adiNorm)
		debug += fmt.Sprintf("PseudoinverseNorm,%e\n", adiNorm)
		fmt.Println(matrix.MatrixLine)
		fmt.Print("\033[0m")
		debug += matrix.MatrixLine + "\n"

		// Add to debug file
		res := fmt.Sprintf("%s,%s", time.Now().Format("2006-01-02 15:04:05"), debug)
		appendToFile(strings.Replace(configPath, ".json", "_debug.csv", 1), res)
	} else {
		// Non-DEBUG: still append debug CSV data silently
		res := fmt.Sprintf("%s,%s", time.Now().Format("2006-01-02 15:04:05"), debug)
		appendToFile(strings.Replace(configPath, ".json", "_debug.csv", 1), res)
	}

	// Single-key Y/N prompt in green. Y will save+flash. N will ask to Restart (R) or Exit (ESC).
	resp := ui.NextYN("Do you want to flash the bars and save the parameters file? (Y/N)")
	switch resp {
	case 'Y':
		saveToJSON(strings.Replace(configPath, ".json", "_calibrated.json", 1), &parameters)
		for {
			if err := flashParameters(bars, &parameters); err != nil {
				log.Printf("Flash error: %v", err)
				// Ask user whether to retry flashing, skip, or exit
				a := ui.NextFlashAction()
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
		// Offer Test (T), Retry (R) or Exit (ESC)
		for {
			choice := ui.NextTestRetryOrExit()
			if choice == 'T' {
				// Run weight check routine (non-destructive)
				if lastParameters != nil && lastParameters.SERIAL != nil {
					// Clear screen and show banner like regular mode, then jump to test
					ui.ClearScreen()
					ui.Greenf("Runrilla Calibration version: %s [build %s]\n", AppVersion, AppBuild)
					ui.Greenf("--------------------------------------------\n")
					ui.DrainKeys()
					// Ensure serial PORT is usable; if not, attempt auto-detect and persist the result
					needDetect := false
					if lastParameters.SERIAL.PORT == "" {
						needDetect = true
					} else {
						cfg := &serial.Config{Name: lastParameters.SERIAL.PORT, Baud: lastParameters.SERIAL.BAUDRATE, Parity: serial.ParityNone, Size: 8, StopBits: serial.Stop1, ReadTimeout: time.Millisecond * 300}
						sp, err := serial.OpenPort(cfg)
						if err != nil {
							needDetect = true
						} else {
							_ = sp.Close()
						}
					}
					if needDetect {
						p := serialpkg.AutoDetectPort(lastParameters)
						if p == "" {
							ui.Warningf("Could not auto-detect serial port for test\n")
							// fall back: try to proceed and let NewLeo485 fail with clear error
						} else {
							lastParameters.SERIAL.PORT = p
							// Persist updated port to JSON so user's config reflects detected port
							persistParameters(configPath, lastParameters)
							ui.Greenf("Auto-detected serial port %s (saved)\n", p)
						}
					}
					// Open serial and run test
					bars := serialpkg.NewLeo485(lastParameters.SERIAL, lastParameters.BARS)
					defer func() { _ = bars.Close() }()
					testWeights(bars, lastParameters)
				} else {
					ui.Warningf("No parameters available for testing\n")
				}
				// after test, loop back to offer options again
				continue
			}
			if choice == 'R' {
				immediateRetry = true
				break
			}
			// ESC or any other -> exit
			os.Exit(0)
		}
	case 27: // ESC
		os.Exit(0)
	}
}

// runTest performs the test mode
func runTest(configPath string) {
	// Clear screen like regular mode
	ui.ClearScreen()
	// Print startup banner similar to regular mode
	ui.Greenf("Runrilla Calibration version: %s [build %s]\n", AppVersion, AppBuild)
	ui.Greenf("--------------------------------------------\n")

	// Load parameters and mirror the serial validation/probe behavior from calRunrilla
	jsonData, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("Error reading file: %v", err)
	}
	var parameters PARAMETERS
	if err := json.Unmarshal(jsonData, &parameters); err != nil {
		log.Fatalf("JSON error: %v", err)
	}
	lastParameters = &parameters
	ui.Debugf(true, "Loaded config: %s (DEBUG=%v)\n", configPath, parameters.DEBUG)

	if parameters.SERIAL == nil {
		log.Fatal("Missing SERIAL section in JSON")
	}
	// Serial validation/auto-detect (same as calRunrilla)
	ui.Debugf(true, "Validating SERIAL configuration...\n")
	needDetect := false
	if parameters.SERIAL.PORT == "" {
		ui.Debugf(true, "Serial PORT missing in JSON, attempting auto-detect...\n")
		needDetect = true
	} else {
		ui.Debugf(true, "Trying configured port: %s (baud %d)\n", parameters.SERIAL.PORT, parameters.SERIAL.BAUDRATE)
		cfg := &serial.Config{Name: parameters.SERIAL.PORT, Baud: parameters.SERIAL.BAUDRATE, Parity: serial.ParityNone, Size: 8, StopBits: serial.Stop1, ReadTimeout: time.Millisecond * 300}
		sp, err := serial.OpenPort(cfg)
		if err != nil {
			log.Printf("Port %s open failed (%v), attempting auto-detect...\n", parameters.SERIAL.PORT, err)
			needDetect = true
		} else {
			_ = sp.Close()
		}
	}
	if needDetect {
		ui.Debugf(true, "Starting serial auto-detect across COM ports (this may take a few seconds)...\n")
		p := serialpkg.AutoDetectPort(&parameters)
		if p == "" {
			log.Fatal("Could not auto-detect serial port")
		}
		parameters.SERIAL.PORT = p
		persistParameters(configPath, &parameters)
		ui.Debugf(true, "Detected serial port: %s (saved to JSON)\n", p)
	}

	ui.Debugf(true, "Opening Leo485 with port %s...\n", parameters.SERIAL.PORT)
	bars := serialpkg.NewLeo485(parameters.SERIAL, parameters.BARS)
	defer func() { _ = bars.Close() }()

	// Probe version and attempt reboot/auto-detect fallback like calRunrilla
	ui.Debugf(true, "Probing device version...\n")
	if !probeVersion(bars, &parameters) {
		log.Printf("No version response from %s. Attempting reboot of all bars...\n", parameters.SERIAL.PORT)
		for i := range bars.Bars {
			if bars.Reboot(i) {
				ui.Greenf("Bar %d reboot command sent\n", i+1)
			} else {
				log.Printf("Bar %d reboot command failed or no response\n", i+1)
			}
			time.Sleep(200 * time.Millisecond)
		}
		ui.Greenf("Waiting for bars to reboot...\n")
		time.Sleep(1500 * time.Millisecond)
		if probeVersion(bars, &parameters) {
			ui.Greenf("Version response received after reboot\n")
		} else {
			log.Printf("No version response from %s after reboot, re-attempting auto-detect...\n", parameters.SERIAL.PORT)
			_ = bars.Close()
			p := serialpkg.AutoDetectPort(&parameters)
			if p != "" && p != parameters.SERIAL.PORT {
				parameters.SERIAL.PORT = p
				persistParameters(configPath, &parameters)
				ui.Debugf(true, "Updated serial port after probe: %s (saved)\n", p)
				bars = serialpkg.NewLeo485(parameters.SERIAL, parameters.BARS)
				defer func() { _ = bars.Close() }()
			}
		}
	}

	// Full version validation (will continue even if minor mismatch)
	if !checkVersion(bars, &parameters) {
		// Version check failed but continue
		ui.Warningf("Warning: version check failed, continuing anyway\n")
	}

	ui.Greenf("\nOpening serial port %s for test...\n", parameters.SERIAL.PORT)
	testWeights(bars, &parameters)
}

// runFlash performs the flash mode
func runFlash(configPath string) {
	// Route the standard logger output through our package-scope redWriter
	log.SetFlags(0)
	log.SetOutput(redWriter{os.Stderr})

	// Flash mode
	ui.ClearScreen()
	ui.Greenf("Runrilla Calibration version: %s [build %s]\n", AppVersion, AppBuild)
	ui.Greenf("--------------------------------------------\n")
	ui.Greenf("Flash mode: loading calibrated parameters from %s\n", configPath)

	jsonData, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("Error reading file: %v", err)
	}
	var parameters PARAMETERS
	if err := json.Unmarshal(jsonData, &parameters); err != nil {
		log.Fatalf("JSON error: %v", err)
	}
	lastParameters = &parameters
	ui.Debugf(true, "Loaded calibrated config: %s\n", configPath)

	// Validate that the file contains calibrated parameters
	if len(parameters.BARS) == 0 || len(parameters.BARS[0].LC) == 0 {
		log.Fatal("The config file does not contain calibrated parameters (LC array is empty). Please use a _calibrated.json file generated after calibration.")
	}

	if parameters.SERIAL == nil {
		log.Fatal("Missing SERIAL section in JSON")
	}
	// Serial validation/auto-detect (same as calRunrilla)
	ui.Debugf(true, "Validating SERIAL configuration...\n")
	needDetect := false
	if parameters.SERIAL.PORT == "" {
		ui.Debugf(true, "Serial PORT missing in JSON, attempting auto-detect...\n")
		needDetect = true
	} else {
		ui.Debugf(true, "Trying configured port: %s (baud %d)\n", parameters.SERIAL.PORT, parameters.SERIAL.BAUDRATE)
		cfg := &serial.Config{Name: parameters.SERIAL.PORT, Baud: parameters.SERIAL.BAUDRATE, Parity: serial.ParityNone, Size: 8, StopBits: serial.Stop1, ReadTimeout: time.Millisecond * 300}
		sp, err := serial.OpenPort(cfg)
		if err != nil {
			log.Printf("Port %s open failed (%v), attempting auto-detect...\n", parameters.SERIAL.PORT, err)
			needDetect = true
		} else {
			_ = sp.Close()
		}
	}
	if needDetect {
		ui.Debugf(true, "Starting serial auto-detect across COM ports (this may take a few seconds)...\n")
		p := serialpkg.AutoDetectPort(&parameters)
		if p == "" {
			log.Fatal("Could not auto-detect serial port")
		}
		parameters.SERIAL.PORT = p
		persistParameters(configPath, &parameters)
		ui.Debugf(true, "Detected serial port: %s (saved to JSON)\n", p)
	}

	ui.Debugf(true, "Opening Leo485 with port %s...\n", parameters.SERIAL.PORT)
	bars := serialpkg.NewLeo485(parameters.SERIAL, parameters.BARS)
	defer func() { _ = bars.Close() }()

	// Probe version and attempt reboot/auto-detect fallback like calRunrilla
	ui.Debugf(true, "Probing device version...\n")
	if !probeVersion(bars, &parameters) {
		log.Printf("No version response from %s. Attempting reboot of all bars...\n", parameters.SERIAL.PORT)
		for i := range bars.Bars {
			if bars.Reboot(i) {
				ui.Greenf("Bar %d reboot command sent\n", i+1)
			} else {
				log.Printf("Bar %d reboot command failed or no response\n", i+1)
			}
			time.Sleep(200 * time.Millisecond)
		}
		ui.Greenf("Waiting for bars to reboot...\n")
		time.Sleep(1500 * time.Millisecond)
		if probeVersion(bars, &parameters) {
			ui.Greenf("Version response received after reboot\n")
		} else {
			log.Printf("No version response from %s after reboot, re-attempting auto-detect...\n", parameters.SERIAL.PORT)
			_ = bars.Close()
			p := serialpkg.AutoDetectPort(&parameters)
			if p != "" && p != parameters.SERIAL.PORT {
				parameters.SERIAL.PORT = p
				persistParameters(configPath, &parameters)
				ui.Debugf(true, "Updated serial port after probe: %s (saved)\n", p)
				bars = serialpkg.NewLeo485(parameters.SERIAL, parameters.BARS)
				defer func() { _ = bars.Close() }()
			}
		}
	}

	// Full version validation (will continue even if minor mismatch)
	if !checkVersion(bars, &parameters) {
		// Version check failed but continue
		ui.Warningf("Warning: version check failed, continuing anyway\n")
	}

	// Display loaded factors and zeros
	nbars := len(parameters.BARS)
	if nbars > 0 {
		nlcs := len(parameters.BARS[0].LC)
		// Show factors
		for i := 0; i < nbars; i++ {
			fmt.Print("\033[38;5;208m")
			fmt.Println(matrix.MatrixLine)
			fmt.Printf("Bar %d factors:\n", i+1)
			for j := 0; j < nlcs; j++ {
				f := parameters.BARS[i].LC[j].FACTOR
				hex := parameters.BARS[i].LC[j].IEEE
				fmt.Printf("[%03d]   % .12f  %s\n", j, float64(f), hex)
			}
			fmt.Println(matrix.MatrixLine)
			fmt.Println()
			fmt.Print("\033[0m")
		}
		// Show zeros
		fmt.Print("\033[38;5;208m")
		fmt.Println(matrix.MatrixLine)
		fmt.Println("zeros (from calibrated file)")
		for i := 0; i < nbars; i++ {
			fmt.Printf("Bar %d zeros:\n", i+1)
			for j := 0; j < nlcs; j++ {
				z := parameters.BARS[i].LC[j].ZERO
				fmt.Printf("[%03d]  %12.0f\n", j, float64(z))
			}
			fmt.Println(matrix.MatrixLine)
		}
		fmt.Print("\033[0m")
	}

	ui.Greenf("\nFlashing bars with calibrated parameters...\n")
	if err := flashParameters(bars, &parameters); err != nil {
		log.Fatalf("Flash failed: %v", err)
	}
	ui.Greenf("All bars flashed successfully!\n")
}

// runMainLoop runs the main calibration loop
func runMainLoop(configPath string) {
	// Route the standard logger output through our package-scope redWriter
	log.SetFlags(0)
	log.SetOutput(redWriter{os.Stderr})

	ui.Debugf(true, "calrunrilla starting with config: %s\n", configPath)

	for {
		ui.ClearScreen()
		// Print application banner after clearing the screen so it remains visible
		ui.Greenf("Runrilla Calibration version: %s [build %s]\n", AppVersion, AppBuild)
		ui.Greenf("--------------------------------------------\n")

		runCalibration(configPath)
		if immediateRetry {
			// reset and immediately restart loop
			immediateRetry = false
			continue
		}

		// Use the green single-key prompt so 'T' (Test), 'R' (Retry) or ESC work without Enter
		choice := ui.NextTestRetryOrExit()
		if choice == 'R' {
			break // restart loop handled by immediateRetry below if needed
		}
		if choice == 'T' {
			// Run weight test using lastParameters if available
			if lastParameters != nil && lastParameters.SERIAL != nil {
				// Clear screen and show banner like regular mode, then jump to test
				ui.ClearScreen()
				ui.Greenf("Runrilla Calibration version: %s [build %s]\n", AppVersion, AppBuild)
				ui.Greenf("--------------------------------------------\n")
				ui.DrainKeys()
				// Ensure serial PORT is usable; if not, attempt auto-detect and persist the result
				needDetect := false
				if lastParameters.SERIAL.PORT == "" {
					needDetect = true
				} else {
					cfg := &serial.Config{Name: lastParameters.SERIAL.PORT, Baud: lastParameters.SERIAL.BAUDRATE, Parity: serial.ParityNone, Size: 8, StopBits: serial.Stop1, ReadTimeout: time.Millisecond * 300}
					sp, err := serial.OpenPort(cfg)
					if err != nil {
						needDetect = true
					} else {
						_ = sp.Close()
					}
				}
				if needDetect {
					p := serialpkg.AutoDetectPort(lastParameters)
					if p == "" {
						ui.Warningf("Could not auto-detect serial port for test\n")
						// fall back: try to proceed and let NewLeo485 fail with clear error
					} else {
						lastParameters.SERIAL.PORT = p
						// Persist updated port to JSON so user's config reflects detected port
						persistParameters(configPath, lastParameters)
						ui.Greenf("Auto-detected serial port %s (saved)\n", p)
					}
				}
				// Open serial and run test
				bars := serialpkg.NewLeo485(lastParameters.SERIAL, lastParameters.BARS)
				defer func() { _ = bars.Close() }()
				testWeights(bars, lastParameters)
			} else {
				ui.Warningf("No parameters available for testing\n")
			}
			// after test, continue outer loop to show banner again
			continue
		}
		// ESC or other: exit
		if choice == 27 {
			break
		}
	}
}
