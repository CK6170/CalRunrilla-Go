package modern

import (
	"fmt"

	"github.com/CK6170/Calrunrilla-go/models"
	serialpkg "github.com/CK6170/Calrunrilla-go/serial"
)

type Session struct {
	Params *models.PARAMETERS
	Bars   *serialpkg.Leo485
}

func Connect(p *models.PARAMETERS) (*Session, error) {
	if p == nil || p.SERIAL == nil {
		return nil, fmt.Errorf("missing SERIAL section")
	}
	bars, err := serialpkg.OpenLeo485(p.SERIAL, p.BARS)
	if err != nil {
		return nil, err
	}
	return &Session{Params: p, Bars: bars}, nil
}

func (s *Session) Close() error {
	if s == nil || s.Bars == nil {
		return nil
	}
	return s.Bars.Close()
}

func ProbeVersion(s *Session) error {
	if s == nil || s.Bars == nil {
		return fmt.Errorf("not connected")
	}
	_, _, _, err := s.Bars.GetVersion(0)
	return err
}

