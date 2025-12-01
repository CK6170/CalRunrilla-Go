package main

// Enums

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
		return "LMR(" + string(int(l)) + ")"
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
		return "FB(" + string(int(f)) + ")"
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
		return "BAY(" + string(int(b)) + ")"
	}
}

// Structs

type PARAMETERS struct {
	SERIAL  *SERIAL  `json:"SERIAL"`
	VERSION *VERSION `json:"VERSION,omitempty"`
	WEIGHT  int      `json:"WEIGHT"`
	AVG     int      `json:"AVG"`
	IGNORE  int      `json:"IGNORE,omitempty"`
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
