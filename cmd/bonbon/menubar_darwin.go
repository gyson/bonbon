//go:build darwin

package main

import (
	"fmt"
	"os"
	"sync/atomic"

	"bonbon/internal/webui"

	"github.com/gogpu/systray"
)

const menuBarSupported = true

// systray locks the initial OS thread during init. Only this child enters AppKit. The
// CLI and server remain independent of the native event loop.
func runNativeMenu(ended <-chan struct{}, openUI, quit func() error) error {
	select {
	case <-ended:
		return nil
	default:
	}
	tray := systray.New()
	menu := systray.NewMenu()
	var busy atomic.Bool
	var openItem, quitItem *systray.MenuItem
	action := func(item *systray.MenuItem, label string, fn func() error, quitting bool) {
		if !busy.CompareAndSwap(false, true) {
			return
		}
		openItem.SetDisabled(true)
		quitItem.SetDisabled(true)
		if quitting {
			item.SetLabel("Quitting BonBon…")
		}
		go func() {
			err := fn()
			select {
			case <-ended:
				return
			default:
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "BonBon menu bar:", err)
				item.SetLabel(label + " — failed; retry")
			} else if quitting {
				return // Keep disabled until the server closes its lifetime pipe.
			} else {
				item.SetLabel(label)
			}
			openItem.SetDisabled(false)
			quitItem.SetDisabled(false)
			busy.Store(false)
		}()
	}
	openItem = menu.Add("Open UI", func() { action(openItem, "Open UI", openUI, false) })
	menu.AddSeparator()
	quitItem = menu.Add("Quit BonBon", func() { action(quitItem, "Quit BonBon", quit, true) })
	tray.SetTemplateIcon(webui.MenuBarIcon).SetTooltip("BonBon").SetMenu(menu).Show()
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ended:
			tray.Remove()
		case <-finished:
		}
	}()
	return tray.Run()
}
