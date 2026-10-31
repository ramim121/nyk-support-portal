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
	consentBox   *walk.Composite
	consentLabel *walk.Label
	disconnectBt *walk.PushButton
	client       clientDisconnector
	deviceID     string

	consent chan bool // set while an Allow/Deny prompt is showing
}

type clientDisconnector interface{ Disconnect() }

var ui *winUI

// platformRun opens the AnyDesk-style status window and runs its message loop
// with the signaling client in the background. The window is a normal taskbar
// window (not always-on-top) when idle, so the customer can use their PC and
// switch windows freely; it only pops to the front for a connection request and
// stays on top while a session is actually active. In headless mode it skips UI.
func platformRun(ctx context.Context, p platformUI) {
	if p.headless {
		p.client.Run(ctx)
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	u := &winUI{client: p.client, deviceID: p.deviceID}
	ui = u

	idText := protocol.FormatDeviceID(p.deviceID)

	// Rasterize brand assets (pure Go) for the header logo and window icon.
	var logoImg d.Widget = d.HSpacer{}
	if img, err := renderLogo(42); err == nil {
		if bmp, err := walk.NewBitmapFromImageForDPI(img, 96); err == nil {
			logoImg = d.ImageView{Image: bmp, Margin: 0}
		}
	}

	var idLabel, idCaption *walk.Label
	err := d.MainWindow{
		AssignTo:   &u.mw,
		Title:      productName,
		Size:       d.Size{Width: 440, Height: 380},
		Layout:     d.VBox{Margins: d.Margins{Left: 24, Top: 20, Right: 24, Bottom: 18}, Spacing: 12},
		Background: d.SolidColorBrush{Color: walk.RGB(255, 255, 255)},
		Children: []d.Widget{
			// Header: logo on the left.
			d.Composite{
				Layout:   d.HBox{MarginsZero: true},
				Children: []d.Widget{logoImg, d.HSpacer{}},
			},
			d.Label{
				Text: "Allow a technician to view and control this computer so they can help you. " +
					"You stay in control and can end the session at any time.",
				Font: d.Font{PointSize: 9},
			},
			d.VSeparator{},

			// Support ID + copy.
			d.Label{AssignTo: &idCaption, Text: "YOUR SUPPORT ID", Font: d.Font{PointSize: 8, Bold: true}},
			d.Composite{
				Layout: d.HBox{MarginsZero: true, Spacing: 10},
				Children: []d.Widget{
					d.Label{AssignTo: &idLabel, Text: idText, Font: d.Font{Family: "Segoe UI", PointSize: 26, Bold: true}},
					d.HSpacer{},
					d.PushButton{Text: "Copy", MaxSize: d.Size{Width: 72}, OnClicked: func() {
						if cb := walk.Clipboard(); cb != nil {
							_ = cb.SetText(idText)
						}
					}},
				},
			},
			d.Label{Text: "Read this ID to your " + productName + " technician.", Font: d.Font{PointSize: 9}},
			d.VSeparator{},

			// Status line.
			d.Composite{
				Layout: d.HBox{MarginsZero: true, Spacing: 8},
				Children: []d.Widget{
					d.Label{AssignTo: &u.statusDot, Text: "●", Font: d.Font{PointSize: 11, Bold: true}},
					d.Label{AssignTo: &u.statusLabel, Text: "Waiting for a support session…", Font: d.Font{PointSize: 10}},
					d.HSpacer{},
				},
			},

			// Inline consent prompt (hidden until a request arrives).
			d.Composite{
				AssignTo: &u.consentBox,
				Visible:  false,
				Layout:   d.VBox{MarginsZero: true, Spacing: 8},
				Children: []d.Widget{
					d.Label{AssignTo: &u.consentLabel, Text: "", Font: d.Font{PointSize: 10, Bold: true}},
					d.Composite{
						Layout: d.HBox{MarginsZero: true, Spacing: 10},
						Children: []d.Widget{
							d.PushButton{Text: "Allow", OnClicked: func() { u.answerConsent(true) }},
							d.PushButton{Text: "Deny", OnClicked: func() { u.answerConsent(false) }},
							d.HSpacer{},
						},
					},
				},
			},

			// End-session button (hidden until a session is active).
			d.PushButton{
				AssignTo:  &u.disconnectBt,
				Text:      "End session",
				Visible:   false,
				OnClicked: func() { u.client.Disconnect() },
			},

			d.VSpacer{},
			d.Label{
				Text: "No one can see or control this PC unless you click Allow. " +
					"Closing this window ends the session immediately.",
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

	// Window / taskbar icon.
	if img, err := renderIcon(64); err == nil {
		if icon, err := walk.NewIconFromImageForDPI(img, 96); err == nil {
			u.mw.SetIcon(icon)
		}
	}

	// Brand colors (declarative widgets don't take text colors directly).
	idLabel.SetTextColor(colNavy)
	idCaption.SetTextColor(colMuted)
	u.statusDot.SetTextColor(colGreen)
	u.statusLabel.SetTextColor(colMuted)

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

// answerConsent is called on the UI thread by the Allow/Deny buttons.
func (u *winUI) answerConsent(allow bool) {
	if u.consent != nil {
		select {
		case u.consent <- allow:
		default:
		}
	}
	u.consentBox.SetVisible(false)
}

// bringToFront restores and focuses the window (used when a request arrives).
func (u *winUI) bringToFront() {
	h := u.mw.Handle()
	win.ShowWindow(h, win.SW_RESTORE)
	win.SetWindowPos(h, win.HWND_TOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE)
	win.SetWindowPos(h, win.HWND_NOTOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE)
	win.SetForegroundWindow(h)
}

// setTopmost pins or unpins the window above others.
func (u *winUI) setTopmost(on bool) {
	after := win.HWND_NOTOPMOST
	if on {
		after = win.HWND_TOPMOST
	}
	win.SetWindowPos(u.mw.Handle(), after, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE)
}

// promptConsent shows the inline Allow/Deny prompt and returns the customer's
// choice (deny on timeout). Called from the signaling goroutine.
func promptConsent(_, tech string) bool {
	u := ui
	if u == nil || u.mw == nil {
		return false
	}
	ch := make(chan bool, 1)
	u.mw.Synchronize(func() {
		u.consent = ch
		u.consentLabel.SetText(tech + " is requesting to connect. Allow this session?")
		u.consentBox.SetVisible(true)
		u.statusDot.SetTextColor(colNavy)
		u.statusLabel.SetTextColor(colNavy)
		u.statusLabel.SetText("A technician is requesting access…")
		u.bringToFront()
	})
	defer func() {
		u.mw.Synchronize(func() {
			u.consent = nil
			u.consentBox.SetVisible(false)
		})
	}()
	select {
	case v := <-ch:
		return v
	case <-time.After(29 * time.Second):
		return false
	}
}

// showBanner turns the window into a visible "being controlled" indicator and
// pins it on top so the customer always sees the session is live.
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
		u.setTopmost(true)
	})
}

// hideBanner restores the idle state (and drops always-on-top) after a session.
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
		u.setTopmost(false)
	})
}

// setClipboard writes text pushed by the technician into the local clipboard.
func setClipboard(text string) {
	if cb := walk.Clipboard(); cb != nil {
		_ = cb.SetText(text)
	}
}
