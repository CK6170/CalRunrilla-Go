package serial

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	models "github.com/CK6170/Calrunrilla-go/models"
	goserial "github.com/tarm/serial"
)

const Euler = "27182818284590452353602874713527\r"

type Leo485 struct {
	Serial       *goserial.Port
	Bars         []*models.BAR
	NLCs         int
	SerialConfig *models.SERIAL
}

func NewLeo485(ser *models.SERIAL, bars []*models.BAR) *Leo485 {
	config := &goserial.Config{
		Name:        ser.PORT,
		Baud:        ser.BAUDRATE,
		Parity:      goserial.ParityNone,
		Size:        8,
		StopBits:    goserial.Stop1,
		ReadTimeout: time.Millisecond * 300,
	}
	port, err := goserial.OpenPort(config)
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

func (l *Leo485) Open() error { return nil }

func (l *Leo485) Close() error { return l.Serial.Close() }

func (l *Leo485) GetADs(index int) ([]uint64, error) {
	cmd := GetCommand(l.Bars[index].ID, []byte(l.SerialConfig.COMMAND))
	response, err := sendCommand(l.Serial, cmd, 200)
	if err != nil {
		return nil, err
	}
	if len(response) == 0 {
		return []uint64{}, nil
	}
	vals, err := parseValues(response, cmd, l.Bars[index].LCS)
	if err != nil {
		return []uint64{}, nil
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
	versionStart := strings.Index(response, "Version ")
	if versionStart == -1 {
		return 0, 0, 0, fmt.Errorf("no version")
	}
	version := strings.TrimSpace(response[versionStart+8:])
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
	data, err := changeState(l.Serial, []byte(Euler), 1000)
	if err != nil {
		return err
	}
	if !strings.Contains(data, "Enter") {
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

// ReadFactors queries a bar for its stored factors using the 'X' read command.
// Response payload format: 4 bytes totalFactor (IEEE754) followed by 4-byte IEEE754 factors
// for each active LC. Returns slice of factors (float64) or an error.
func (l *Leo485) ReadFactors(index int) ([]float64, error) {
	cmd := GetCommand(l.Bars[index].ID, []byte("X"))
	// Send command and get raw bytes (no textual parsing)
	raw, err := sendCommand(l.Serial, cmd, 300)
	if err != nil {
		return nil, fmt.Errorf("ReadFactors sendCommand error: %v", err)
	}
	if len(raw) < 6 {
		return nil, fmt.Errorf("ReadFactors: response too short: %d bytes", len(raw))
	}

	// find CRLF or LF
	rnPos := bytes.Index(raw, []byte("\r\n"))
	if rnPos == -1 {
		rnPos = bytes.IndexByte(raw, '\n')
	}
	if rnPos == -1 {
		return nil, fmt.Errorf("ReadFactors: no line terminator in response; len=%d", len(raw))
	}

	// Validate ID bytes (first two bytes of response should match cmd[:2])
	if len(raw) < 2 || raw[0] != cmd[0] || raw[1] != cmd[1] {
		// provide a hex dump for diagnostics
		hexParts := make([]string, 0, len(raw))
		for _, b := range raw {
			hexParts = append(hexParts, fmt.Sprintf("%02X", b))
		}
		return nil, fmt.Errorf("ReadFactors GetData error: wrong ID or missing pipe; raw_len=%d raw_hex=%s", len(raw), strings.Join(hexParts, " "))
	}

	if rnPos < 2 {
		return nil, fmt.Errorf("ReadFactors: response too short before CRC/terminator")
	}

	// CRC is the two bytes immediately before CR/LF
	if rnPos < 2 {
		return nil, fmt.Errorf("ReadFactors: no CRC present")
	}
	receivedCRC := raw[rnPos-2 : rnPos]
	dataForCRC := raw[:rnPos-2]
	calc := crc16(dataForCRC)
	if receivedCRC[0] != calc[0] || receivedCRC[1] != calc[1] {
		// hex dump for diagnostics
		hexParts := make([]string, 0, len(raw))
		for _, b := range raw {
			hexParts = append(hexParts, fmt.Sprintf("%02X", b))
		}
		return nil, fmt.Errorf("ReadFactors CRC mismatch: expected=%02X%02X got=%02X%02X raw_hex=%s", calc[0], calc[1], receivedCRC[0], receivedCRC[1], strings.Join(hexParts, " "))
	}

	// payload starts right after the 2-byte ID (no ASCII pipe expected for binary payloads)
	payload := raw[2 : rnPos-2]
	nlcs := l.NLCs
	expected := 4 * (1 + nlcs) // total + each factor (4 bytes each)
	if len(payload) < expected {
		return nil, fmt.Errorf("ReadFactors: payload too short: got %d, want %d", len(payload), expected)
	}

	ofs := 4 // skip totalFactor (first 4 bytes)
	factors := make([]float64, nlcs)
	for i := 0; i < nlcs; i++ {
		if ofs+4 > len(payload) {
			return nil, fmt.Errorf("ReadFactors: payload truncated for factor %d", i)
		}
		bits := binary.BigEndian.Uint32(payload[ofs : ofs+4])
		f32 := math.Float32frombits(bits)
		factors[i] = float64(f32)
		ofs += 4
	}
	return factors, nil
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

// The lower-level serial helpers are implemented in com.go in this package.
