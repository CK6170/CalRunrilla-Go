package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/tarm/serial"
)

const Euler = "27182818284590452353602874713527\r"

type Leo485 struct {
	Serial       *serial.Port
	Bars         []*BAR
	NLCs         int
	SerialConfig *SERIAL
}

func NewLeo485(ser *SERIAL, bars []*BAR) *Leo485 {
	config := &serial.Config{
		Name:        ser.PORT,
		Baud:        ser.BAUDRATE,
		Parity:      serial.ParityNone,
		Size:        8,
		StopBits:    serial.Stop1,
		ReadTimeout: time.Millisecond * 300,
	}
	port, err := serial.OpenPort(config)
	if err != nil {
		log.Fatal(err)
	}
	l := &Leo485{
		Serial:       port,
		Bars:         bars,
		SerialConfig: ser,
	}
	l.NLCs = numOfActiveLCs(bars[0].LCS)
	for _, bar := range bars {
		if numOfActiveLCs(bar.LCS) != l.NLCs {
			log.Fatal("Number of Load Cells per bar must match")
		}
	}
	return l
}

func (l *Leo485) Open() error {
	return nil // Already opened
}

func (l *Leo485) Close() {
	l.Serial.Close()
}

func (l *Leo485) GetADs(index int) ([]uint64, error) {
	cmd := GetCommand(l.Bars[index].ID, []byte(l.SerialConfig.COMMAND)) // Use command from JSON config
	response, err := sendCommand(l.Serial, cmd, 200)                    // Increased timeout from 20 to 200
	if err != nil {
		return nil, err
	}
	if len(response) == 0 {
		return []uint64{}, nil // Return empty slice instead of error
	}
	vals, err := parseValues(response, cmd, l.Bars[index].LCS)
	if err != nil {
		return []uint64{}, nil // Return empty slice instead of error for now
	}
	bruts := make([]uint64, len(vals))
	for i, v := range vals {
		bruts[i] = uint64(v.brut)
	}
	return bruts, nil
}

func (l *Leo485) GetVersion(index int) (int, int, int, error) {
	cmd := GetCommand(l.Bars[index].ID, []byte("V"))
	response, err := getData(l.Serial, cmd, 200)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("GetVersion error: %v", err)
	}
	if !strings.Contains(response, "Version") {
		return 0, 0, 0, fmt.Errorf("no version")
	}
	// Extract version string after "Version "
	versionStart := strings.Index(response, "Version ")
	if versionStart == -1 {
		return 0, 0, 0, fmt.Errorf("no version")
	}
	version := strings.TrimSpace(response[versionStart+8:]) // Skip "Version "
	parts := strings.Split(version, ".")
	if len(parts) < 3 {
		return 0, 0, 0, fmt.Errorf("invalid version")
	}
	id, _ := strconv.Atoi(parts[0])
	major, _ := strconv.Atoi(parts[1])
	minor, _ := strconv.Atoi(parts[2])
	return id, major, minor, nil
}

func (l *Leo485) WriteZeros(index int, zeros []float64, total uint64) bool {
	sb := "O"
	k := 0
	for i := 0; i < 4; i++ {
		if (l.Bars[index].LCS & (1 << i)) != 0 {
			sb += fmt.Sprintf("%09.0f|", zeros[k])
			k++
		} else {
			sb += fmt.Sprintf("%09d|", 0)
		}
	}
	sb += fmt.Sprintf("%09d|", total)
	cmd := GetCommand(l.Bars[index].ID, []byte(sb))
	response, err := updateValue(l.Serial, cmd, 200)
	if err != nil {
		return false
	}
	return strings.Contains(response, "OK")
}

func (l *Leo485) WriteFactors(index int, factors []float64) bool {
	sb := "X"
	k := 0
	for i := 0; i < 4; i++ {
		if (l.Bars[index].LCS & (1 << i)) != 0 {
			sb += fmt.Sprintf("%.10f|", factors[k])
			k++
		} else {
			sb += "1.0000000000|"
		}
	}
	cmd := GetCommand(l.Bars[index].ID, []byte(sb))
	response, err := updateValue(l.Serial, cmd, 200)
	if err != nil {
		return false
	}
	return strings.Contains(response, "OK")
}

func (l *Leo485) OpenToUpdate() error {
	// Use the lower-level changeState to read raw bytes, then analyze them.
	data, err := changeState(l.Serial, []byte(Euler), 1000)
	if err != nil {
		return err
	}
	// If the expected human-readable marker isn't present, include a hex dump
	// and length so we can see non-printable or truncated bytes.
	if !strings.Contains(data, "Enter") {
		// Convert to []byte for hex dumping
		raw := []byte(data)
		hexParts := make([]string, 0, len(raw))
		for _, b := range raw {
			hexParts = append(hexParts, fmt.Sprintf("%02X", b))
		}
		hexDump := strings.Join(hexParts, " ")
		return fmt.Errorf("no enter: raw_len=%d raw_hex=%s raw_str=%q", len(raw), hexDump, strings.TrimSpace(data))
	}
	return nil
}

func (l *Leo485) Reboot(index int) bool {
	cmd := GetCommand(l.Bars[index].ID, []byte("R"))
	response, err := changeState(l.Serial, cmd, 200)
	if err != nil {
		return false
	}
	return strings.Contains(response, "Rebooting")
}

func numOfActiveLCs(lcs byte) int {
	count := 0
	for i := 0; i < 8; i++ {
		if (lcs & (1 << i)) != 0 {
			count++
		}
	}
	return count
}

func sendCommand(sp *serial.Port, cmd []byte, timeout int) ([]byte, error) {
	// Write the command then read until CRLF or timeout
	if _, err := sp.Write(cmd); err != nil {
		return nil, err
	}
	// Small initial delay to give device time to reply
	time.Sleep(time.Millisecond * time.Duration(timeout/2))
	return readUntil(sp, timeout)
}

// readUntil reads from the serial port until a CRLF ("\r\n") is seen or the
// timeout (in milliseconds) elapses. It returns the accumulated bytes.
func readUntil(sp *serial.Port, timeout int) ([]byte, error) {
	deadline := time.Now().Add(time.Millisecond * time.Duration(timeout))
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 256)
	for time.Now().Before(deadline) {
		n, err := sp.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			s := string(buf)
			// Accept CRLF or LF-only terminators (some devices send '\n' only)
			if strings.Contains(s, "\r\n") || strings.Contains(s, "\n") {
				return buf, nil
			}
		}
		if err != nil {
			return buf, err
		}
		// brief sleep to avoid busy loop
		time.Sleep(10 * time.Millisecond)
	}
	// timeout but return what we have (caller can decide)
	// Include a small hex dump to help debugging
	hexParts := make([]string, 0, len(buf))
	for _, b := range buf {
		hexParts = append(hexParts, fmt.Sprintf("%02X", b))
	}
	hexDump := strings.Join(hexParts, " ")
	return buf, fmt.Errorf("read timeout; got %d bytes; raw_hex=%s", len(buf), hexDump)
}

func getData(sp *serial.Port, cmd []byte, timeout int) (string, error) {
	data, err := sendCommand(sp, cmd, timeout)
	if err != nil {
		return "", err
	}
	result, err := checkData(data, cmd)
	return result, err
}

func updateValue(sp *serial.Port, cmd []byte, timeout int) (string, error) {
	data, err := sendCommand(sp, cmd, timeout)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func changeState(sp *serial.Port, cmd []byte, timeout int) (string, error) {
	data, err := sendCommand(sp, cmd, timeout)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseValues(input []byte, cmd []byte, lcs byte) ([]struct {
	lc   int
	brut uint64
}, error) {
	data, err := checkData(input, cmd)
	if err != nil {
		return nil, err
	}
	inputs := strings.Split(data, "|")
	vals := []struct {
		lc   int
		brut uint64
	}{}
	for i, in := range inputs {
		if (lcs & (1 << i)) != 0 {
			brut, _ := strconv.ParseUint(in, 10, 64)
			vals = append(vals, struct {
				lc   int
				brut uint64
			}{i, brut})
		}
	}
	return vals, nil
}

func checkData(input []byte, cmd []byte) (string, error) {
	sinput := string(input)
	if len(sinput) < 5 {
		return "", fmt.Errorf("short response")
	}
	if !(len(sinput) > 2 && sinput[:2] == string(cmd[:2]) && sinput[2] == '|') {
		return "", fmt.Errorf("wrong ID or missing pipe")
	}
	// Accept CRLF or LF-only line endings
	rnPos := strings.Index(sinput, "\r\n")
	if rnPos == -1 {
		// try LF-only
		rnPos = strings.Index(sinput, "\n")
	}
	if rnPos == -1 {
		return "", fmt.Errorf("wrong format")
	}
	if rnPos < 2 {
		return "", fmt.Errorf("wrong format")
	}
	receivedCRC := input[rnPos-2 : rnPos]
	dataForCRC := input[:rnPos-2]
	calculatedCRC := crc16(dataForCRC)
	if receivedCRC[0] != calculatedCRC[0] || receivedCRC[1] != calculatedCRC[1] {
		return "", fmt.Errorf("wrong checksum")
	}
	result := sinput[3 : rnPos-2]
	return result, nil
}
