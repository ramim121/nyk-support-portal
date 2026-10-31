//go:build windows

// Command console is the NYK technician desktop app. It is a thin native window
// (WebView2, pure Go, no cgo) that loads the console web UI served by your
// server at https://<domain>/console. Because the page is loaded from the
// server, all its API/WebSocket calls use the right origin automatically.
//
// The server URL is baked in at build time:
//
//	go build -ldflags "-H windowsgui -X main.ServerURL=https://support.example.com/console"
//
// and can be overridden at runtime with -url.
package main

import (
	"flag"
	"log"

	"github.com/jchv/go-webview2"
)

// ServerURL is set at build time via -ldflags "-X main.ServerURL=...".
var ServerURL = "https://support.example.com/console"

func main() {
	url := flag.String("url", ServerURL, "console web app URL to open")
	flag.Parse()

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "NYK Support Portal — Console",
			Width:  1366,
			Height: 860,
			Center: true,
		},
	})
	if w == nil {
		log.Fatal("Failed to initialise WebView2. Install the Microsoft Edge WebView2 Runtime and try again.")
	}
	defer w.Destroy()
	w.Navigate(*url)
	w.Run()
}
