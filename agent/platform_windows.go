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

// Product name shown in the agent window and dialogs.
const productName = "NYK Advance Support"

// Brand colors (match the console theme).
var (
	colNavy  = walk.RGB(45, 61, 127)
	colGreen = walk.RGB(22, 163, 74)
	colRed   = walk.RGB(220, 38, 38)
	colMuted = walk.RGB(104, 113, 138)
)

// winUI holds the live widgets so the consent/banner hooks (called from the
// signaling goroutine) can drive them via MainWindow.Synchronize.
type winUI struct {
	mw           *walk.MainWindow
	statusDot    *walk.Label
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

	var header, idLabel, idCaption *walk.Label
	err := d.MainWindow{
		AssignTo:   &u.mw,
		Title:      productName,
		Size:       d.Size{Width: 460, Height: 300},
		Layout:     d.VBox{Margins: d.Margins{Left: 22, Top: 18, Right: 22, Bottom: 18}, Spacing: 10},
		Background: d.SolidColorBrush{Color: walk.RGB(255, 255, 255)},
		Children: []d.Widget{
			d.Label{AssignTo: &header, Text: productName, Font: d.Font{Family: "Segoe UI", PointSize: 15, Bold: true}},
			d.VSeparator{},
			d.Label{AssignTo: &idCaption, Text: "Your support ID — read this to your technician:"},
			d.Label{AssignTo: &idLabel, Text: idText, Font: d.Font{Family: "Segoe UI", PointSize: 26, Bold: true}},
			d.Composite{
				Layout: d.HBox{MarginsZero: true, Spacing: 8},
				Children: []d.Widget{
					d.Label{AssignTo: &u.statusDot, Text: "●", Font: d.Font{PointSize: 11, Bold: true}},
					d.Label{AssignTo: &u.statusLabel, Text: "Waiting for a support session…", Font: d.Font{PointSize: 10}},
					d.HSpacer{},
				},
			},
			d.PushButton{
				AssignTo:  &u.disconnectBt,
				Text:      "End session",
				Visible:   false,
				OnClicked: func() { u.client.Disconnect() },
			},
			d.VSpacer{},
			d.Label{
				Text: "Keep this window open during support. No one can see or control this PC " +
					"unless you click Allow. Closing this window ends the session immediately.",
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

	// Apply brand colors (declarative widgets don't take text colors directly).
	header.SetTextColor(colNavy)
	idLabel.SetTextColor(colNavy)
	idCaption.SetTextColor(colMuted)
	u.statusDot.SetTextColor(colGreen)
	u.statusLabel.SetTextColor(colMuted)

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
			"Only click Yes if you are expecting support from " + productName + ".\n\nAllow this session?"
		r := walk.MsgBox(u.mw, productName+" — Allow session?", msg,
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
		u.statusDot.SetTextColor(colRed)
		u.statusLabel.SetTextColor(colRed)
		u.statusLabel.SetText("Your screen is being controlled by " + tech)
		u.disconnectBt.SetVisible(true)
		u.mw.SetTitle(productName + " — CONNECTED")
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
		u.statusDot.SetTextColor(colGreen)
		u.statusLabel.SetTextColor(colMuted)
		u.statusLabel.SetText("Waiting for a support session…")
		u.disconnectBt.SetVisible(false)
		u.mw.SetTitle(productName)
	})
}

// setClipboard writes text pushed by the technician into the local clipboard.
func setClipboard(text string) {
	if cb := walk.Clipboard(); cb != nil {
		_ = cb.SetText(text)
	}
}
