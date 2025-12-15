package modern

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/CK6170/Calrunrilla-go/models"
	serialpkg "github.com/CK6170/Calrunrilla-go/serial"
)

func LoadParameters(path string) (*models.PARAMETERS, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p models.PARAMETERS
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	if p.SERIAL == nil {
		return nil, fmt.Errorf("missing SERIAL section in JSON")
	}
	if len(p.BARS) == 0 {
		return nil, fmt.Errorf("no BARS defined in JSON")
	}
	// Match CLI behavior: if IGNORE not provided, use AVG.
	if p.IGNORE <= 0 {
		p.IGNORE = p.AVG
	}
	return &p, nil
}

func PersistParameters(path string, p *models.PARAMETERS) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// EnsureSerialPort auto-detects the serial port if missing and optionally persists it back
// into the original config file (to match existing CLI behavior).
func EnsureSerialPort(configPath string, p *models.PARAMETERS, persist bool) (changed bool, err error) {
	if p == nil || p.SERIAL == nil {
		return false, fmt.Errorf("missing SERIAL section")
	}
	if strings.TrimSpace(p.SERIAL.PORT) != "" {
		return false, nil
	}
	port := serialpkg.AutoDetectPort(p)
	if port == "" {
		return false, fmt.Errorf("could not auto-detect serial port")
	}
	p.SERIAL.PORT = port
	if persist {
		if err := PersistParameters(configPath, p); err != nil {
			return true, err
		}
	}
	return true, nil
}

// CalibratedPath derives the default calibrated json path from the base config path.
func CalibratedPath(configPath string) string {
	if strings.HasSuffix(strings.ToLower(configPath), "_calibrated.json") {
		return configPath
	}
	if strings.HasSuffix(strings.ToLower(configPath), ".json") {
		return strings.TrimSuffix(configPath, ".json") + "_calibrated.json"
	}
	return configPath + "_calibrated.json"
}

