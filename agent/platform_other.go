//go:build !windows

package main

import (
	"context"
	"log"

	"github.com/nyk/support-portal/agent/capture"
	"github.com/nyk/support-portal/agent/input"
)

// platformRun just runs the signaling client on non-Windows platforms (dev/CI);
// there is no desktop UI here.
func platformRun(ctx context.Context, ui platformUI) {
	ui.client.Run(ctx)
}

// Non-Windows fallbacks. The production agent targets Windows; on other
// platforms (dev/CI) the agent always runs with the fake source + log sink, so
// these exist only to satisfy the compiler and are never the primary path.

func newPlatformSource() capture.Source { return capture.NewFake() }

func newPlatformSink() input.Sink { return input.NewLogSink() }

// promptConsent is the default (non-headless) consent path. Without a desktop
// UI on this platform it denies by default; headless/auto flags override it.
func promptConsent(_, tech string) bool {
	log.Printf("consent: no desktop UI on this platform; denying request from %q", tech)
	return false
}

func showBanner(tech string) { log.Printf("[banner] This PC is being controlled by %q", tech) }
func hideBanner()            { log.Printf("[banner] hidden") }
func setClipboard(string)    {}
