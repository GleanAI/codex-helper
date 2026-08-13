package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"codex-helper/internal/app"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		addr := os.Getenv("LISTEN_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		if addr[0] == ':' {
			addr = "127.0.0.1" + addr
		}
		r, e := http.Get("http://" + addr + "/health/live")
		if e != nil || r.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}
	a, err := app.New()
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()
	go func() {
		if err := a.Run(); err != nil {
			log.Fatal(err)
		}
	}()
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c
}
