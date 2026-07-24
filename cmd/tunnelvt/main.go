// Command tunnelvt exposes a local port through the gotunnel server.
//
// No login — auto-fetches JWT on first run, saves to ~/.tunnelvt.json.
//
// Usage:
//
//	tunnelvt -app myapp -port 3000
package main

import (
	"flag"
	"log"
	"os"

	"github.com/vintechid/tunnelvt-go/pkg/client"
)

func main() {
	app := flag.String("app", "", "app name for this tunnel")
	port := flag.Int("port", 0, "local port to expose")
	flag.Parse()

	if *app == "" {
		log.Fatal("-app is required")
	}
	if *port == 0 {
		log.Fatal("-port is required")
	}

	c := client.New(*app, *port)
	if err := c.Connect(); err != nil {
		log.Printf("[tunnelvt] error: %v", err)
		os.Exit(1)
	}
}
