//go:build windows

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	wsOverlapped     = 0x00000000
	wsCaption        = 0x00C00000
	wsSysMenu        = 0x00080000
	wsMinimize       = 0x00020000
	wsVisible        = 0x10000000
	wsChild          = 0x40000000
	bsPushButton     = 0x0000000B // BS_OWNERDRAW
	wsTabStop        = 0x00010000
	wmDestroy        = 2
	wmPaint          = 0x000F
	wmEraseBkgnd     = 0x0014
	wmCtlColorStatic = 0x0138
	wmDrawItem       = 0x002B
	wmCommand        = 0x111
	wmClose          = 0x10
	wmSetFont        = 0x30
	wmAppState       = 0x8001
	wmAppNotice      = 0x8002
	swShow           = 5
	swHide           = 0
	mbYesNo          = 0x24
	idYes            = 6
	mbError          = 0x10
	mbInformation    = 0x40
	idInstallBotc    = 101
	idInstallGrim    = 102
	idLaunch         = 103
	idRefresh        = 104
	idOfficial       = 105
	idLogs           = 106
	idMode           = 108
	idUninstall      = 109
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	versionDll           = syscall.NewLazyDLL("version.dll")
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procShowWindow       = user32.NewProc("ShowWindow")
	procUpdateWindow     = user32.NewProc("UpdateWindow")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procPostMessageW     = user32.NewProc("PostMessageW")
	procSetWindowTextW   = user32.NewProc("SetWindowTextW")
	procEnableWindow     = user32.NewProc("EnableWindow")
	procSendMessageW     = user32.NewProc("SendMessageW")
	procLoadIconW        = user32.NewProc("LoadIconW")
	procLoadImageW       = user32.NewProc("LoadImageW")
	procInvalidateRect   = user32.NewProc("InvalidateRect")
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procGetStockObject   = syscall.NewLazyDLL("gdi32.dll").NewProc("GetStockObject")
)

type point struct{ X, Y int32 }
type message struct {
	Hwnd           uintptr
	Msg            uint32
	_              uint32
	WParam, LParam uintptr
	Time           uint32
	Pt             point
	Private        uint32
}
type wndClassEx struct {
	Size                               uint32
	Style                              uint32
	WndProc                            uintptr
	ClsExtra, WndExtra                 int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	IconSmall                          uintptr
}
type nativeUI struct {
	root, desktop string
	first         bool
	hwnd          uintptr
	labels        map[string]uintptr
	buttons       map[int]uintptr
	mu            sync.Mutex
	pending       stateSnapshot
	current       stateSnapshot
	busy          bool
	notice        string
	noticeError   bool
	autoAttempted bool
	uninstalled   bool
	theme         *nativeTheme
}

var activeUI *nativeUI

func toW(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }
func showMessage(hwnd uintptr, title, body string, flags uintptr) uintptr {
	r, _, _ := user32.NewProc("MessageBoxW").Call(hwnd, uintptr(unsafe.Pointer(toW(body))), uintptr(unsafe.Pointer(toW(title))), flags)
	return r
}
func confirm(hwnd uintptr, body string) bool {
	return showMessage(hwnd, "Grim Poster", body, mbYesNo) == idYes
}
func setText(h uintptr, s string) { procSetWindowTextW.Call(h, uintptr(unsafe.Pointer(toW(s)))) }
func enable(h uintptr, enabled bool) {
	v := uintptr(0)
	if enabled {
		v = 1
	}
	procEnableWindow.Call(h, v)
}
func post(h uintptr, msg uint32) { procPostMessageW.Call(h, uintptr(msg), 0, 0) }
func createControl(parent uintptr, class, text string, x, y, w, h int32, id int, style uint32) uintptr {
	handle, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(toW(class))), uintptr(unsafe.Pointer(toW(text))), uintptr(style|wsChild|wsVisible), uintptr(x), uintptr(y), uintptr(w), uintptr(h), parent, uintptr(id), 0, 0)
	font, _, _ := procGetStockObject.Call(17) // DEFAULT_GUI_FONT
	procSendMessageW.Call(handle, wmSetFont, font, 1)
	return handle
}
func (ui *nativeUI) label(id string, text string, x, y, w, h int32) {
	ui.labels[id] = createControl(ui.hwnd, "STATIC", text, x, y, w, h, 0, 0)
	if ui.theme != nil {
		ui.theme.styleLabel(id, ui.labels[id])
	}
}
func (ui *nativeUI) button(id int, text string, x, y, w, h int32) {
	ui.buttons[id] = createControl(ui.hwnd, "BUTTON", text, x, y, w, h, id, bsPushButton|wsTabStop)
	if ui.theme != nil {
		ui.theme.styleButton(ui.buttons[id])
	}
}
func (ui *nativeUI) setLabel(id, s string) {
	if h := ui.labels[id]; h != 0 {
		setText(h, s)
	}
}
func (ui *nativeUI) setButton(id int, s string, enabled bool) {
	if h := ui.buttons[id]; h != 0 {
		setText(h, s)
		enable(h, enabled)
	}
}
func (ui *nativeUI) updateModeUI(s stateSnapshot) {
	ui.setLabel("mode", "Native capture · runtime loading")
	ui.setButton(idMode, "Native capture", false)
}

func wndCallback(hwnd uintptr, msg uint32, wparam, lparam uintptr) (returnValue uintptr) {
	// Win32 callbacks aren't protected by main's defer. Recover here so a
	// callback exception leaves a local panic stack and shuts down the GUI.
	defer func() {
		if r := recover(); r != nil {
			recordPanic(fmt.Sprint(r), debug.Stack())
			updateCrashStage("win32_callback_panic")
			procPostQuitMessage.Call(1)
			returnValue = 0
		}
	}()
	switch msg {
	case wmEraseBkgnd:
		return 1
	case wmPaint:
		if activeUI != nil && activeUI.hwnd == hwnd && activeUI.theme != nil {
			activeUI.theme.paint(hwnd)
			return 0
		}
	case wmCtlColorStatic:
		if activeUI != nil && activeUI.hwnd == hwnd && activeUI.theme != nil {
			return activeUI.theme.colorStatic(wparam, lparam, activeUI.labels)
		}
	case wmDrawItem:
		if activeUI != nil && activeUI.hwnd == hwnd && activeUI.theme != nil {
			activeUI.theme.drawButton(lparam)
			return 1
		}
	case wmCommand:
		if activeUI != nil && activeUI.hwnd == hwnd {
			activeUI.command(int(wparam & 0xffff))
		}
		return 0
	case wmAppState:
		if activeUI != nil {
			activeUI.onRefresh()
		}
		return 0
	case wmAppNotice:
		if activeUI != nil {
			activeUI.onNotice()
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wparam, lparam)
	return r
}
func launchManager(root, desktop string, first bool) error {
	// Every window, child control, and GetMessage/DispatchMessage loop must use
	// the SAME OS thread. Go goroutines are otherwise free to migrate between
	// OS threads, which can strand Win32 messages and yield "Not Responding".
	// Keep the thread pinned until the native GUI has fully shut down.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	updateCrashStage("creating_native_window")
	ui := &nativeUI{root: root, desktop: desktop, first: first, labels: make(map[string]uintptr), buttons: make(map[int]uintptr), theme: newNativeTheme()}
	activeUI = ui
	className := toW("GrimPosterNativeManager")
	instance, _, _ := procGetModuleHandleW.Call(0)
	icon, _, _ := procLoadIconW.Call(instance, uintptr(1))
	wc := wndClassEx{Size: uint32(unsafe.Sizeof(wndClassEx{})), WndProc: syscall.NewCallback(wndCallback), Instance: instance, Icon: icon, IconSmall: icon, ClassName: className, Background: ui.theme.brushBackground}
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("could not register native interface: %w", err)
	}
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(toW("Grim Poster | App Manager v"+bootstrapVersion))), wsCaption|wsSysMenu|wsMinimize|wsOverlapped, 0x80000000, 0x80000000, 825, 790, 0, 0, instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("could not create native interface: %w", err)
	}
	ui.hwnd = hwnd
	// The themed cards and branding are rendered natively via GDI; the live
	// version labels and clickable controls stay native for keyboard access.
	ui.label("installed", "Checking...", 48, 201, 207, 40)
	ui.label("official", "Checking...", 311, 201, 207, 40)
	ui.label("grim", "Checking...", 574, 201, 201, 40)
	ui.label("detail", "Checking the official BOTC installation...", 49, 296, 715, 44)
	ui.button(idInstallBotc, "Install / Update BOTC", 30, 385, 370, 46)
	ui.button(idInstallGrim, "Install / Repair Grim Poster", 415, 385, 374, 46)
	ui.button(idLaunch, "Launch BOTC with Grim Poster", 30, 446, 759, 56)
	ui.button(idRefresh, "Refresh", 30, 516, 111, 38)
	ui.button(idOfficial, "Official releases", 153, 516, 149, 38)
	ui.button(idLogs, "View logs", 314, 516, 300, 38)
	ui.button(idUninstall, "Uninstall Grim Poster", 626, 516, 163, 38)
	ui.button(idMode, "Capture: Adaptive", 530, 569, 250, 35)
	ui.label("mode", "Detecting capture mode...", 48, 576, 460, 30)
	ui.label("status", "Checking for verified releases...", 48, 633, 725, 39)
	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)
	recordDiagnostic("native_gui_window_created", nil)
	ui.refresh()
	updateCrashStage("native_manager_running")
	if recoveredPreviousCrash() {
		showMessage(ui.hwnd, "Grim Poster - Recovery", "A previous Grim Poster process ended unexpectedly.\n\nA recovery report is saved under View logs as last-unexpected-exit.txt.\n\nYou can review it using View logs. Diagnostic logs stay on this device and are not uploaded by Grim Poster.", mbInformation)
	}
	recordDiagnostic("native_gui_message_loop_entered", nil)
	var msg message
	for {
		r, _, callErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) == -1 {
			return fmt.Errorf("native Windows message loop failed: %w", callErr)
		}
		if r == 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	recordDiagnostic("native_gui_message_loop_exited", nil)
	if msg.WParam != 0 {
		return errors.New("native interface stopped after a callback failure; see panic-local-only.log")
	}
	return nil
}
func (ui *nativeUI) refresh() {
	ui.mu.Lock()
	if ui.busy {
		ui.mu.Unlock()
		return
	}
	ui.busy = true
	ui.mu.Unlock()
	recordDiagnostic("manager_refresh_started", nil)
	ui.setLabel("status", "Checking the installed game and official releases...")
	enable(ui.buttons[idRefresh], false)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				recordPanic(fmt.Sprint(r), debug.Stack())
				ui.mu.Lock()
				ui.busy = false
				ui.notice = "An update check failed unexpectedly. The crash has been saved locally."
				ui.noticeError = true
				ui.mu.Unlock()
				post(ui.hwnd, wmAppNotice)
			}
		}()
		snapshot := stateSnapshot{}
		var errorsList []string
		p, err := readPatch(ui.desktop)
		if err != nil {
			errorsList = append(errorsList, "Patch: "+err.Error())
		}
		snapshot.Patch = p
		botc, err := getBOTC()
		if err != nil {
			errorsList = append(errorsList, "BOTC: "+err.Error())
		}
		snapshot.Installed = botc
		if botc != nil && p.PatchVerification == "derive-from-verified-original" && p.ExpectedPatchedSha256 == "" {
			resolved, resolveErr := resolveLocalPatchSHA(ui.desktop, botc.Path, p)
			if resolveErr != nil {
				errorsList = append(errorsList, "Local patch: "+resolveErr.Error())
				snapshot.Patch = patchManifest{}
			} else {
				snapshot.Patch = resolved
				p = resolved
			}
		}
		client := httpClient()
		ctx, cancel := context.WithTimeout(context.Background(), 24*time.Second)
		defer cancel()
		type githubResult struct {
			release *officialRelease
			err     error
		}
		type signedResult struct {
			release *signedRelease
			err     error
		}
		githubChannel := make(chan githubResult, 1)
		signedChannel := make(chan signedResult, 1)
		go func() { r, e := getOfficial(ctx, client); githubChannel <- githubResult{r, e} }()
		go func() { signedChannel <- signedResult{} }() // Legacy patch releases are incompatible with runtime injection.
		officialResult := <-githubChannel
		if officialResult.err != nil {
			errorsList = append(errorsList, "GitHub: "+officialResult.err.Error())
		}
		snapshot.Official = officialResult.release
		sigResult := <-signedChannel
		if sigResult.err != nil {
			errorsList = append(errorsList, "Signed feed: "+sigResult.err.Error())
		}
		if sigResult.release != nil {
			snapshot.Remote = makeRemote(sigResult.release, snapshot.Installed, p)
		}

		snapshot.Error = strings.Join(errorsList, " | ")
		ui.mu.Lock()
		ui.pending = snapshot
		ui.busy = false
		ui.mu.Unlock()
		recordDiagnostic("manager_refresh_finished", nil)
		post(ui.hwnd, wmAppState)
	}()
}
func (ui *nativeUI) onRefresh() {
	ui.mu.Lock()
	ui.current = ui.pending
	s := ui.current
	ui.mu.Unlock()
	if s.Installed != nil {
		ui.setLabel("installed", "v"+s.Installed.Version)
	} else {
		ui.setLabel("installed", "Not installed")
	}
	if s.Official != nil {
		ui.setLabel("official", "v"+s.Official.Version)
	} else {
		ui.setLabel("official", "Unavailable")
	}
	if s.Remote != nil {
		ui.setLabel("grim", "v"+s.Remote.Release.ModVersion)
	} else {
		ui.setLabel("grim", "v0.2.0 runtime")
	}
	if s.Remote != nil && s.Remote.UpdateAvailable {
		ui.setLabel("detail", "A compatible, signed Grim Poster update is available.")
	} else if s.patchPresent() {
		ui.setLabel("detail", "BOTC is verified. Grim Poster will load into its window when launched.")
	} else if s.canUseLocalPatch() {
		ui.setLabel("detail", "BOTC needs a clean official installation. Uninstall the old Grim Poster patch or reinstall BOTC.")
	} else if s.Installed != nil {
		ui.setLabel("detail", "This BOTC version has not been verified for this Grim Poster build.")
	} else {
		ui.setLabel("detail", "Install Blood on the Clocktower Online first.")
	}
	if s.Official != nil {
		if s.Installed == nil || versionCmp(s.Installed.Version, s.Official.Version) < 0 {
			ui.setButton(idInstallBotc, "Install / Update BOTC v"+s.Official.Version, true)
		} else {
			ui.setButton(idInstallBotc, "BOTC is up to date", false)
		}
	} else {
		ui.setButton(idInstallBotc, "GitHub unavailable", false)
	}
	ui.setButton(idInstallGrim, "Runtime companion v0.2.0", false)
	if s.Remote != nil && s.Remote.UpdateAvailable {
		ui.setButton(idInstallGrim, "Install Grim Poster v"+s.Remote.Release.ModVersion, true)
	}
	ui.setButton(idLaunch, "Launch BOTC with Grim Poster", s.canLaunch())
	enable(ui.buttons[idRefresh], true)
	enable(ui.buttons[idOfficial], true)
	enable(ui.buttons[idLogs], true)
	enable(ui.buttons[idUninstall], true)
	ui.updateModeUI(s)
	if s.Error != "" {
		ui.setLabel("status", s.Error)
	} else {
		if s.Installed != nil && !safeToAutoLaunch(loadAdaptive(ui.root), s.Installed.Version, legacyAvailable(ui.desktop)) {
			ui.setLabel("status", "Capture needs attention. Automatic launch paused; open View logs or change capture mode.")
		} else {
			ui.setLabel("status", "Ready. Installer downloads and updates require confirmation.")
		}
	}
	if !ui.autoAttempted {
		ui.autoAttempted = true
		if !ui.first && s.ready() && s.Installed != nil && safeToAutoLaunch(loadAdaptive(ui.root), s.Installed.Version, legacyAvailable(ui.desktop)) {
			ui.launchBOTC(true)
		}
	}
}
func (ui *nativeUI) busyAction(label string, action func() error) {
	ui.mu.Lock()
	if ui.busy {
		ui.mu.Unlock()
		return
	}
	ui.busy = true
	ui.mu.Unlock()
	for _, h := range ui.buttons {
		enable(h, false)
	}
	ui.setLabel("status", label)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				recordPanic(fmt.Sprint(r), debug.Stack())
				ui.mu.Lock()
				ui.busy = false
				ui.notice = "An operation stopped unexpectedly. A local crash record was saved."
				ui.noticeError = true
				ui.mu.Unlock()
				post(ui.hwnd, wmAppNotice)
			}
		}()
		err := action()
		ui.mu.Lock()
		ui.busy = false
		if err != nil {
			recordDiagnostic("operation_failed", err)
			ui.notice = err.Error()
			ui.noticeError = true
		} else {
			if ui.uninstalled {
				ui.notice = "Grim Poster has been uninstalled. BOTC Online remains installed. Logs and screenshots were kept.\n\nYou can delete any remaining GrimPoster.exe after this window closes. Previously downloaded launcher copies must be deleted manually."
			} else {
				ui.notice = "Operation completed. Status will refresh."
			}
			ui.noticeError = false
		}
		ui.mu.Unlock()
		post(ui.hwnd, wmAppNotice)
	}()
}
func (ui *nativeUI) onNotice() {
	ui.mu.Lock()
	msg, isError := ui.notice, ui.noticeError
	ui.mu.Unlock()
	if msg != "" {
		if isError {
			showMessage(ui.hwnd, "Grim Poster - Error", msg, mbError)
		} else {
			showMessage(ui.hwnd, "Grim Poster", msg, mbInformation)
		}
	}
	ui.mu.Lock()
	uninstalled := ui.uninstalled
	ui.mu.Unlock()
	if uninstalled {
		procDestroyWindow.Call(ui.hwnd)
		return
	}
	ui.refresh()
}
func (ui *nativeUI) command(id int) {
	ui.mu.Lock()
	busy, s := ui.busy, ui.current
	ui.mu.Unlock()
	if busy {
		return
	}
	switch id {
	case idRefresh:
		ui.refresh()
	case idMode:
		s := loadAdaptive(ui.root)
		if err := setPreference(ui.root, nextMode(s.Preference)); err != nil {
			showMessage(ui.hwnd, "Grim Poster", "Unable to save compatibility setting: "+err.Error(), mbError)
			return
		}
		ui.updateModeUI(ui.current)
	case idOfficial:
		exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", officialReleases).Start()
	case idLogs:
		dir := diagnosticDirectory()
		os.MkdirAll(dir, 0700)
		exec.Command("explorer.exe", dir).Start()
	case idUninstall:
		if processExists("botc-app.exe") {
			showMessage(ui.hwnd, "Grim Poster", "Close BOTC Online before uninstalling Grim Poster. The game will remain installed.", mbInformation)
			return
		}
		if !confirm(ui.hwnd, "Uninstall Grim Poster from this Windows user account?\n\n- Remove Grim Poster support files and settings.\n- Keep BOTC Online, screenshots, and diagnostic logs.\n\nContinue?") {
			return
		}
		ui.busyAction("Removing Grim Poster support files...", func() error {
			kept, err := removeManagedSupport(ui.root)
			if err != nil {
				return err
			}
			if len(kept) > 0 {
				recordDiagnostic("uninstall_launcher_file_locked", nil)
			}
			ui.mu.Lock()
			ui.uninstalled = true
			ui.mu.Unlock()
			return nil
		})
	case idInstallBotc:
		if s.Official == nil {
			return
		}
		if !confirm(ui.hwnd, "Download and run the official BOTC Online v"+s.Official.Version+" installer from The Pandemonium Institute?\n\nThe installer will be verified against GitHub's published SHA-256 digest.") {
			return
		}
		ui.busyAction("Downloading and verifying the official BOTC installer...", func() error { return downloadAndRunOfficial(*s.Official) })
	case idInstallGrim:
		if s.Installed == nil {
			return
		}
		if !confirm(ui.hwnd, "Install a compatible Grim Poster patch? Close BOTC Online before continuing.") {
			return
		}
		ui.busyAction("Installing Grim Poster (see logs if it fails)...", func() error { return installGrim(ui.desktop, s) })
	case idLaunch:
		ui.launchBOTC(false)
	}
}
func (ui *nativeUI) launchBOTC(automatic bool) {
	ui.mu.Lock()
	s := ui.current
	ui.mu.Unlock()
	if !s.patchPresent() {
		if !automatic {
			showMessage(ui.hwnd, "Grim Poster", "A verified official BOTC 3.58.0 installation is required. Use the old Grim Poster uninstaller or reinstall the official game first.", mbInformation)
		}
		return
	}
	if processExists("botc-app.exe") {
		if !automatic {
			showMessage(ui.hwnd, "Grim Poster", "BOTC is already running. Close it before launching with the companion.", mbInformation)
		}
		return
	}
	if s.Remote != nil && s.Remote.UpdateAvailable {
		return
	}
	updateCrashStage("starting_companion")
	mode, err := startAdaptiveCompanion(ui.root, ui.desktop, s.Installed.Version)
	if err != nil {
		recordDiagnostic("companion_launch_failed", err)
		showMessage(ui.hwnd, "Grim Poster", "Could not start the companion:\n"+err.Error()+"\n\nUse View logs in the manager to review the local troubleshooting details.", mbError)
		return
	}
	recordDiagnostic("companion_started_"+mode, nil)
	procDestroyWindow.Call(ui.hwnd)
}

func downloadAndRunOfficial(r officialRelease) error {
	if processExists("botc-app.exe") {
		return errors.New("close BOTC before updating the game")
	}
	dir := filepath.Join(os.TempDir(), "GrimPosterOfficial")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, "Blood.on.the.Clocktower.Online_"+r.Version+"_x64-setup.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := httpClient()
	client.Timeout = 5 * time.Minute
	b, err := readExactHTTP(ctx, client, r.URL, r.Size)
	if err != nil {
		return err
	}
	if !isExpectedDownload(b, r.SHA256, r.Size) {
		return errors.New("official installer failed its SHA-256 or size verification")
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		return err
	}
	return exec.Command(path).Start()
}
func installGrim(desktop string, s stateSnapshot) error {
	return errors.New("This release uses runtime loading. Reinstall the official game if it was previously patched.")
}
func startNativeCompanion(root string) error {
	own, err := os.Executable()
	if err != nil {
		return err
	}
	logDir := filepath.Join(root, "logs")
	if err = os.MkdirAll(logDir, 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(logDir, "native-launch-"+time.Now().Format("20060102-150405")+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	cmd := exec.Command(own, "--companion")
	cmd.Stdout = file
	cmd.Stderr = file
	if err = cmd.Start(); err != nil {
		recordDiagnostic("companion_spawn_failed", err)
		file.Close()
		return err
	}
	// Catch an immediate companion failure before dismissing the manager.
	// The child also writes its own crash diagnostics, even after the parent exits.
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		if err != nil {
			recordDiagnostic("companion_exited", err)
			fmt.Fprintf(file, "Native companion exited: %v\n", err)
			_ = file.Sync()
		}
		_ = file.Close()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("companion ended immediately: %w (see local logs)", err)
		}
		return errors.New("companion closed unexpectedly immediately after startup")
	case <-time.After(750 * time.Millisecond):
		return nil
	}
}

func processExists(name string) bool {
	cmd := exec.Command("tasklist.exe", "/FI", "IMAGENAME eq "+name, "/FO", "CSV", "/NH")
	cmd.SysProcAttr = hiddenWindow()
	b, e := cmd.Output()
	return e == nil && strings.Contains(strings.ToLower(string(b)), strings.ToLower(name))
}

func getBOTC() (*installedBOTC, error) {
	paths := []string{}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		paths = append(paths, filepath.Join(local, "Blood on the Clocktower Online", "botc-app.exe"), filepath.Join(local, "Programs", "Blood on the Clocktower Online", "botc-app.exe"), filepath.Join(local, "botc-app", "botc-app.exe"))
		if custom, err := os.ReadFile(filepath.Join(local, "Programs", "Grim Poster", "botc-path.txt")); err == nil {
			paths = append(paths, strings.TrimSpace(string(custom)))
		}
	}
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		paths = append(paths, filepath.Join(pf, "Blood on the Clocktower Online", "botc-app.exe"))
	}
	if pf := os.Getenv("ProgramFiles(x86)"); pf != "" {
		paths = append(paths, filepath.Join(pf, "Blood on the Clocktower Online", "botc-app.exe"))
	}
	// Query uninstall registry in a separate process, not PowerShell. This handles custom installer directories.
	for _, root := range []string{`HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall`, `HKLM\Software\Microsoft\Windows\CurrentVersion\Uninstall`, `HKLM\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`} {
		c := exec.Command("reg.exe", "query", root, "/s", "/f", "Blood on the Clocktower Online", "/d")
		c.SysProcAttr = hiddenWindow()
		b, err := c.Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			key := strings.TrimSpace(line)
			if !strings.HasPrefix(key, "HKEY_") {
				continue
			}
			c := exec.Command("reg.exe", "query", key, "/v", "InstallLocation")
			c.SysProcAttr = hiddenWindow()
			raw, err := c.Output()
			if err == nil {
				for _, l := range strings.Split(string(raw), "\n") {
					if strings.Contains(l, "REG_SZ") {
						parts := strings.SplitN(l, "REG_SZ", 2)
						if len(parts) == 2 {
							paths = append(paths, filepath.Join(strings.TrimSpace(parts[1]), "botc-app.exe"))
						}
					}
				}
			}
		}
	}
	var best *installedBOTC
	seen := map[string]bool{}
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(strings.Trim(path, `"`)))
		if !hasPEName(path) || seen[strings.ToLower(path)] {
			continue
		}
		seen[strings.ToLower(path)] = true
		if _, err := os.Stat(path); err != nil {
			continue
		}
		version, err := getFileProductVersion(path)
		if err != nil || !verRE.MatchString(version) {
			continue
		}
		if best != nil && versionCmp(version, best.Version) <= 0 {
			continue
		}
		hash, err := hashFile(path)
		if err != nil {
			continue
		}
		best = &installedBOTC{Path: path, Version: version, SHA256: hash}
	}
	return best, nil
}
func getFileProductVersion(path string) (string, error) {
	wide := toW(path)
	size, _, _ := versionDll.NewProc("GetFileVersionInfoSizeW").Call(uintptr(unsafe.Pointer(wide)), 0)
	if size == 0 {
		return "", errors.New("version metadata unavailable")
	}
	data := make([]byte, size)
	ret, _, _ := versionDll.NewProc("GetFileVersionInfoW").Call(uintptr(unsafe.Pointer(wide)), 0, size, uintptr(unsafe.Pointer(&data[0])))
	if ret == 0 {
		return "", errors.New("failed to read version metadata")
	}
	slash := toW(`\`)
	var info uintptr
	var length uint32
	ret, _, _ = versionDll.NewProc("VerQueryValueW").Call(uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(slash)), uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&length)))
	if ret == 0 || length < 24 {
		return "", errors.New("invalid file version data")
	}
	base := uintptr(unsafe.Pointer(&data[0]))
	if info < base || info-base > uintptr(len(data)) || uintptr(length) > uintptr(len(data))-(info-base) {
		return "", errors.New("version metadata pointer outside the supplied buffer")
	}
	offset := int(info - base)
	fixed := data[offset : offset+int(length)]
	if binary.LittleEndian.Uint32(fixed[:4]) != 0xFEEF04BD {
		return "", errors.New("bad fixed version header")
	}
	ms := binary.LittleEndian.Uint32(fixed[16:20])
	ls := binary.LittleEndian.Uint32(fixed[20:24])
	version := fmt.Sprintf("%d.%d.%d", ms>>16, ms&0xffff, ls>>16)
	return version, nil
}
