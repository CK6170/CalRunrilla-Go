package serial

import (
	"fmt"
	"strings"
	"time"

	"github.com/CK6170/Calrunrilla-go/models"
	"github.com/tarm/serial"
)

// AutoDetectPort scans common COM ports to find one responding to a Version command.
func AutoDetectPort(parameters *models.PARAMETERS) string {
	expectedFirstBarID := parameters.BARS[0].ID
	baud := parameters.SERIAL.BAUDRATE
	// Scan COM1..COM64
	for i := 1; i <= 64; i++ {
		portName := fmt.Sprintf("COM%d", i)
		if TestPort(portName, expectedFirstBarID, baud) {
			return portName
		}
	}
	return ""
}

// TestPort tries to open port and issue a version command to first bar ID.
func TestPort(name string, barID int, baud int) bool {
	config := &serial.Config{Name: name, Baud: baud, Parity: serial.ParityNone, Size: 8, StopBits: serial.Stop1, ReadTimeout: time.Millisecond * 300}
	sp, err := serial.OpenPort(config)
	if err != nil {
		return false
	}
	defer func() { _ = sp.Close() }()

	cmd := GetCommand(barID, []byte("V"))
	resp, err := GetData(sp, cmd, 200)
	if err != nil {
		return false
	}
	return strings.Contains(resp, "Version")
}
