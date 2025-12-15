package modern

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/CK6170/Calrunrilla-go/models"
)

// SaveCalibratedJSON writes a _calibrated.json file compatible with the existing CLI.
// It intentionally does not print to stdout/stderr (UI should surface errors itself).
func SaveCalibratedJSON(path string, p *models.PARAMETERS) error {
	if p == nil {
		return fmt.Errorf("parameters nil")
	}
	payload := struct {
		SERIAL *models.SERIAL `json:"SERIAL"`
		BARS   []*models.BAR  `json:"BARS"`
		AVG    int           `json:"AVG"`
		IGNORE int           `json:"IGNORE"`
		DEBUG  bool          `json:"DEBUG"`
	}{
		SERIAL: p.SERIAL,
		BARS:   p.BARS,
		AVG:    p.AVG,
		IGNORE: p.IGNORE,
		DEBUG:  p.DEBUG,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}
	verFile := strings.TrimSuffix(path, ".json") + ".version"
	_ = os.WriteFile(verFile, []byte("modernui local\n"), 0644)
	return nil
}

