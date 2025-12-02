package serial

import (
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

// GetDeviceFactors queries the device with command 'X' for factors and parses
// the response containing IEEE754 floats. Returns a slice of float64 of length
// l.NLCs or an error on failure.
func (l *Leo485) GetDeviceFactors(index int) ([]float64, error) {
	cmd := GetCommand(l.Bars[index].ID, []byte("X"))
	resp, err := changeState(l.Serial, cmd, 300)
	if err != nil || len(resp) == 0 {
		return nil, err
	}
	b := []byte(resp)
	nlcs := l.NLCs

	anchorBE := []byte{0x3F, 0x80, 0x00, 0x00}
	anchorLE := []byte{0x00, 0x00, 0x80, 0x3F}
	for i := 0; i+4 <= len(b); i++ {
		if i+4+4*nlcs <= len(b) && (equalBytes(b[i:i+4], anchorBE) || equalBytes(b[i:i+4], anchorLE)) {
			start := i + 4
			vals := make([]float64, nlcs)
			useLE := equalBytes(b[i:i+4], anchorLE)
			ok := true
			for j := 0; j < nlcs; j++ {
				off := start + j*4
				chunk := b[off : off+4]
				var u uint32
				if useLE {
					u = binary.LittleEndian.Uint32(chunk)
				} else {
					u = binary.BigEndian.Uint32(chunk)
				}
				f := math.Float32frombits(u)
				ff := float64(f)
				if math.IsNaN(ff) || math.IsInf(ff, 0) || math.Abs(ff) > 1e6 {
					ok = false
					break
				}
				vals[j] = float64(f)
			}
			if ok {
				return vals, nil
			}
		}
	}

	needed := 4 * nlcs
	for start := 0; start+needed <= len(b); start++ {
		window := b[start : start+needed]
		valsBE := make([]float64, nlcs)
		valsLE := make([]float64, nlcs)
		validBE := true
		validLE := true
		anyReasonableBE := false
		anyReasonableLE := false
		for i := 0; i < nlcs; i++ {
			chunk := window[i*4 : i*4+4]
			uBE := binary.BigEndian.Uint32(chunk)
			uLE := binary.LittleEndian.Uint32(chunk)
			fBE := math.Float32frombits(uBE)
			fLE := math.Float32frombits(uLE)
			valsBE[i] = float64(fBE)
			valsLE[i] = float64(fLE)
			if math.IsNaN(valsBE[i]) || math.IsInf(valsBE[i], 0) || math.Abs(valsBE[i]) > 1e6 {
				validBE = false
			}
			if math.IsNaN(valsLE[i]) || math.IsInf(valsLE[i], 0) || math.Abs(valsLE[i]) > 1e6 {
				validLE = false
			}
			if math.Abs(valsBE[i]) > 1e-6 {
				anyReasonableBE = true
			}
			if math.Abs(valsLE[i]) > 1e-6 {
				anyReasonableLE = true
			}
		}
		if validBE && anyReasonableBE {
			return valsBE, nil
		}
		if validLE && anyReasonableLE {
			return valsLE, nil
		}
	}
	return nil, fmt.Errorf("no valid factors found in response")
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

// equalBytes compares two byte slices for equality (helper internal to serial package)
func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The lower-level serial helpers are implemented in com.go in this package.
