package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/CK6170/Calrunrilla-go/internal/server"
)

func main() {
	var (
		addr = flag.String("addr", "127.0.0.1:8080", "http listen address")
		web  = flag.String("web", "./web", "path to web root (index.html)")
	)
	flag.Parse()

	// Ensure we serve the configured web root by chdir so ./web works regardless of launch dir.
	if err := os.Chdir(filepath.Clean(filepath.Dir(os.Args[0]))); err != nil {
		// ignore; best effort
	}
	// Prefer user-provided web root when running from repo root.
	if st, err := os.Stat(*web); err == nil && st.IsDir() {
		_ = os.Chdir(".")
	}

	s := server.New()
	log.Printf("Serving on http://%s", *addr)
	log.Printf("UI:        http://%s/", *addr)
	if err := http.ListenAndServe(*addr, s.Handler()); err != nil {
		fmt.Println(err)
	}
}

