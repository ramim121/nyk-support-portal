//go:build windows

package main

import (
	"context"
	"log"
	"time"

	"github.com/lxn/walk"
	d "github.com/lxn/walk/declarative"
	"github.com/lxn/win"

	"github.com/nyk/support-portal/agent/capture"
	"github.com/nyk/support-portal/agent/input"
	"github.com/nyk/support-portal/protocol"
)

// Real Windows backends.
func newPlatformSource() capture.Source { return capture.NewWindows() }
func newPlatformSink() input.Sink       { return input.NewWindows() }

// winUI holds the live widgets so the consent/banner hooks (called from the
// signaling goroutine) can drive them via MainWindow.Synchronize.
type winUI struct {
	mw           *walk.MainWindow
	statusLabel  *walk.Label
	disconnectBt *walk.PushButton
	client       clientDisconnector
}

type clientDisconnector interface{ Disconnect() }

var ui *winUI

// platformRun opens the always-visible status window and runs its message loop
// with the signaling client in the background. Closing the window ends any
// session and exits. In headless mode it skips the UI.
func platformRun(ctx context.Context, p platformUI) {
	if p.headless {
		p.client.Run(ctx)
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	u := &winUI{client: p.client}
	ui = u

	idText := protocol.FormatDeviceID(p.deviceID)

	err := d.MainWindow{
		AssignTo: &u.mw,
		Title:    "NYK Support Portal",
		Size:     d.Size{Width: 440, Height: 250},
		Layout:   d.VBox{Margins: d.Margins{Left: 16, Top: 16, Right: 16, Bottom: 16}},
		Children: []d.Widget{
			d.Label{Text: "NYK Support Portal", Font: d.Font{PointSize: 14, Bold: true}},
			d.Label{Text: "Your support ID — read this to your NYK technician:"},
			d.Label{Text: idText, Font: d.Font{PointSize: 22, Bold: true}},
			d.Label{AssignTo: &u.statusLabel, Text: "Waiting for a support session…"},
			d.PushButton{
				AssignTo:  &u.disconnectBt,
				Text:      "Disconnect",
				Visible:   false,
				OnClicked: func() { u.client.Disconnect() },
			},
			d.VSpacer{},
			d.Label{
				Text: "Keep this window open during support. No one can see or control " +
					"this PC unless you click Allow. Closing this window ends the session.",
				Font: d.Font{PointSize: 8},
			},
		},
	}.Create()
	if err != nil {
		log.Printf("ui: create window failed: %v; falling back to headless", err)
		ui = nil
		p.client.Run(ctx)
		return
	}

	// Keep the window above others so the customer always sees it.
	win.SetWindowPos(u.mw.Handle(), win.HWND_TOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE)

	// Run the client in the background.
	go p.client.Run(runCtx)

	// If the context is cancelled (Ctrl-C/SIGTERM), close the window to exit.
	go func() {
		<-runCtx.Done()
		if u.mw != nil {
			u.mw.Synchronize(func() { u.mw.Close() })
		}
	}()

	u.mw.Run() // blocks until the window closes
	cancel()   // closing the window ends any session
}

// promptConsent shows the modal Allow/Deny dialog on the UI thread and returns
// the customer's choice (deny on timeout). Called from the signaling goroutine.
func promptConsent(_, tech string) bool {
	u := ui
	if u == nil || u.mw == nil {
		return false
	}
	resp := make(chan bool, 1)
	u.mw.Synchronize(func() {
		msg := tech + " is requesting to view and control this computer.\n\n" +
			"Only click Allow if you are expecting support from NYK.\n\nAllow this session?"
		r := walk.MsgBox(u.mw, "NYK Support Portal — Allow session?", msg,
			walk.MsgBoxYesNo|walk.MsgBoxIconWarning|walk.MsgBoxDefButton2)
		resp <- r == win.IDYES
	})
	select {
	case v := <-resp:
		return v
	case <-time.After(29 * time.Second):
		return false
	}
}

// showBanner turns the window into a visible "being controlled" indicator.
func showBanner(tech string) {
	u := ui
	if u == nil || u.mw == nil {
		return
	}
	u.mw.Synchronize(func() {
		u.statusLabel.SetText("● Your screen is being controlled by " + tech)
		u.disconnectBt.SetVisible(true)
		u.mw.SetTitle("NYK Support Portal — CONTROLLED")
		win.SetWindowPos(u.mw.Handle(), win.HWND_TOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE)
	})
}

// hideBanner restores the idle state after a session ends.
func hideBanner() {
	u := ui
	if u == nil || u.mw == nil {
		return
	}
	u.mw.Synchronize(func() {
		u.statusLabel.SetText("Waiting for a support session…")
		u.disconnectBt.SetVisible(false)
		u.mw.SetTitle("NYK Support Portal")
	})
}

// setClipboard writes text pushed by the technician into the local clipboard.
func setClipboard(text string) {
	if cb := walk.Clipboard(); cb != nil {
		_ = cb.SetText(text)
	}
}
