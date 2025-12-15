package server

import (
	"fmt"
	"time"

	"github.com/CK6170/Calrunrilla-go/models"
	serialpkg "github.com/CK6170/Calrunrilla-go/serial"
	goserial "github.com/tarm/serial"
)

// openBars opens the serial port and constructs a Leo485 without using serial.NewLeo485
// (which log.Fatal's on error).
func openBars(ser *models.SERIAL, bars []*models.BAR) (*serialpkg.Leo485, error) {
	if ser == nil {
		return nil, fmt.Errorf("missing SERIAL")
	}
	if ser.PORT == "" {
		return nil, fmt.Errorf("missing SERIAL.PORT")
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("no BARS configured")
	}

	cfg := &goserial.Config{
		Name:        ser.PORT,
		Baud:        ser.BAUDRATE,
		Parity:      goserial.ParityNone,
		Size:        8,
		StopBits:    goserial.Stop1,
		ReadTimeout: time.Millisecond * 300,
	}
	port, err := goserial.OpenPort(cfg)
	if err != nil {
		return nil, err
	}

	l := &serialpkg.Leo485{
		Serial:       port,
		Bars:         bars,
		NLCs:         countActiveLCs(bars[0].LCS),
		SerialConfig: ser,
	}
	if l.NLCs <= 0 {
		_ = port.Close()
		return nil, fmt.Errorf("invalid LCS bitmask on first bar")
	}
	for _, b := range bars {
		if countActiveLCs(b.LCS) != l.NLCs {
			_ = port.Close()
			return nil, fmt.Errorf("number of active load cells per bar must match")
		}
	}
	return l, nil
}

func countActiveLCs(lcs byte) int {
	n := 0
	for i := 0; i < 8; i++ {
		if (lcs & (1 << i)) != 0 {
			n++
		}
	}
	return n
}

