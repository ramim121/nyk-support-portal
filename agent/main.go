// Command agent is the NYK Support Portal client. In production it runs on a
// customer's Windows PC, shows a consent prompt and an on-screen banner, and
// shares the screen under a technician's control — only while the customer
// allows it.
//
// This file is the cross-platform core and a headless dev/CI runner. The real
// Windows capture + input backends and the consent window are added in
// platform-specific files; when they are absent (e.g. on Linux, or with
// -fake-capture) the agent uses an embedded test pattern and a logging input
// sink, which exercises the entire network pipeline without touching a desktop.
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/nyk/support-portal/agent/capture"
	"github.com/nyk/support-portal/agent/input"
	"github.com/nyk/support-portal/agent/signaling"
	"github.com/nyk/support-portal/protocol"
)

// Version is overridden at build time with -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	var (
		server     = flag.String("server", envOr("NYK_SERVER", "ws://localhost:8080"), "server base URL (ws:// or wss://), no path")
		orgKey     = flag.String("orgkey", envOr("NYK_ORG_KEY", ""), "shared org key")
		id         = flag.String("id", envOr("NYK_DEVICE_ID", ""), "9-digit device id (generated if empty)")
		hostname   = flag.String("hostname", "", "hostname to report (defaults to OS hostname)")
		headless   = flag.Bool("headless", false, "run without a window (dev/CI)")
		fakeCap    = flag.Bool("fake-capture", false, "use the embedded test pattern instead of real capture")
		autoAccept = flag.Bool("auto-accept", false, "automatically allow sessions (dev only)")
		autoDeny   = flag.Bool("auto-deny", false, "automatically deny sessions (for testing the deny path)")
	)
	flag.Parse()

	deviceID := *id
	if deviceID == "" {
		deviceID = randomDeviceID()
	}
	if len(deviceID) != 9 {
		log.Fatalf("device id must be 9 digits, got %q", deviceID)
	}

	hn := *hostname
	if hn == "" {
		hn, _ = os.Hostname()
	}

	// Choose backends. On non-Windows, or when -fake-capture is set, use the
	// embedded test pattern + logging sink.
	useFake := *fakeCap || runtime.GOOS != "windows"

	var sourceFactory func() capture.Source
	var sink input.Sink
	if useFake {
		sourceFactory = func() capture.Source { return capture.NewFake() }
		sink = input.NewLogSink()
		log.Printf("agent: using FAKE capture (test pattern) + logging input sink")
	} else {
		// Real Windows backends are provided by platform files; see
		// newPlatformSource / newPlatformSink. If unavailable this returns an
		// error at session start.
		sourceFactory = func() capture.Source { return newPlatformSource() }
		sink = newPlatformSink()
		log.Printf("agent: using real desktop capture + input")
	}

	// Report the monitors the chosen source sees in the hello.
	monitors := sourceFactory().Monitors()

	hello := protocol.Hello{
		DeviceID: deviceID,
		Hostname: hn,
		Username: currentUser(),
		OS:       runtime.GOOS,
		Version:  Version,
		Monitors: monitors,
	}

	consent := makeConsentFunc(*headless, *autoAccept, *autoDeny)

	client := signaling.New(signaling.Deps{
		ServerURL: strings.TrimRight(*server, "/"),
		OrgKey:    *orgKey,
		Hello:     hello,
		Consent:   consent,
		NewSource: sourceFactory,
		Sink:      sink,
		OnSessionStart: func(tech string) {
			log.Printf("=== SESSION ACTIVE: controlled by %q ===", tech)
			showBanner(tech)
		},
		OnSessionEnd: func() {
			log.Printf("=== SESSION ENDED ===")
			hideBanner()
		},
		OnClipboardFromConsole: func(text string) {
			log.Printf("clipboard from console: %d chars", len(text))
			setClipboard(text)
		},
	})

	fmt.Printf("NYK Support Portal — this PC's ID: %s\n", protocol.FormatDeviceID(deviceID))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client.Run(ctx)
}

// makeConsentFunc returns the consent callback. In a headed build this would
// pop the Allow/Deny dialog; headless uses the flags.
func makeConsentFunc(headless, autoAccept, autoDeny bool) func(string, string) bool {
	if autoDeny {
		return func(_, tech string) bool {
			log.Printf("consent: auto-DENY request from %q", tech)
			return false
		}
	}
	if autoAccept || headless {
		return func(_, tech string) bool {
			log.Printf("consent: auto-ALLOW request from %q", tech)
			return true
		}
	}
	// Default: delegate to the platform consent UI.
	return promptConsent
}

func randomDeviceID() string {
	var b strings.Builder
	for i := 0; i < 9; i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(10))
		b.WriteByte(byte('0' + n.Int64()))
	}
	return b.String()
}

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	return "user"
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
