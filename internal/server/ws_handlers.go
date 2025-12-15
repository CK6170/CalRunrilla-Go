package server

import (
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// local app; allow all
		return true
	},
}

func (s *Server) handleWSTest(w http.ResponseWriter, r *http.Request) {
	s.handleWSHub(w, r, s.wsTest)
}

func (s *Server) handleWSCal(w http.ResponseWriter, r *http.Request) {
	s.handleWSHub(w, r, s.wsCal)
}

func (s *Server) handleWSFlash(w http.ResponseWriter, r *http.Request) {
	s.handleWSHub(w, r, s.wsFlash)
}

func (s *Server) handleWSHub(w http.ResponseWriter, r *http.Request, hub *WSHub) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := hub.Add(conn)

	// Keep reading until client disconnects
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			hub.Remove(client)
			return
		}
	}
}

