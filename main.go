package main

import (
	"flag"
	"log"

	"kenichi-explicit-server/internal/config"
	"kenichi-explicit-server/internal/server"
)

func main() {
	dev := flag.Bool("dev", false, "Enable development mode (no disk writes, placeholder data, relaxed auth)")
	flag.Parse()

	cfg := config.Load(*dev)

	// Run the public server in a goroutine; if it dies the whole process exits via log.Fatal.
	go func() {
		if err := server.RunPublic(cfg); err != nil {
			log.Fatalf("[public] %v", err)
		}
	}()

	// Block on the private server.
	if err := server.RunPrivate(cfg); err != nil {
		log.Fatalf("[private] %v", err)
	}
}
