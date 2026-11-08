//go:build windows

package main

// Minimal Win32/GDI theming only: no WebView, extra installer framework or
// third-party dependencies. Button messages still flow to nativeUI.command.

import (
	"syscall"
	"unsafe"
)

type nativeRect struct{ Left, Top, Right, Bottom int32 }
type paintStruct struct {
	HDC       uintptr
	Erase     int32
	Update    nativeRect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}
type drawItem struct {
	CtlType, CtlID, ItemID, Action, State uint32
	Hwnd, DC                              uintptr
	Rect                                  nativeRect
	Data                                  uintptr
}
type nativeTheme struct {
	brushBackground, brushCard, brushDetail, brushMuted                   uintptr
	fontTitle, fontSubtitle, fontSection, fontCard, fontButton, fontSmall uintptr
	logo                                                                  uintptr
}

var (
	gdi32                 = syscall.NewLazyDLL("gdi32.dll")
	gdCreateSolidBrush    = gdi32.NewProc("CreateSolidBrush")
	gdCreatePen           = gdi32.NewProc("CreatePen")
	gdDeleteObject        = gdi32.NewProc("DeleteObject")
	gdSelectObject        = gdi32.NewProc("SelectObject")
	gdCreateFont          = gdi32.NewProc("CreateFontW")
	gdSetBkMode           = gdi32.NewProc("SetBkMode")
	gdSetTextColor        = gdi32.NewProc("SetTextColor")
	gdRoundRect           = gdi32.NewProc("RoundRect")
	gdFillRect            = user32.NewProc("FillRect")
	gdBeginPaint          = user32.NewProc("BeginPaint")
	gdEndPaint            = user32.NewProc("EndPaint")
	gdDrawText            = user32.NewProc("DrawTextW")
	gdDrawIconEx          = user32.NewProc("DrawIconEx")
	gdGetClientRect       = user32.NewProc("GetClientRect")
	gdGetWindowText       = user32.NewProc("GetWindowTextW")
	gdGetWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	gdDrawFocusRect       = user32.NewProc("DrawFocusRect")
)

func rgb(r, g, b byte) uintptr { return uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16 }

var (
	colBackground  = rgb(15, 14, 25)
	colHeader      = rgb(27, 23, 40)
	colCard        = rgb(36, 32, 51)
	colDetail      = rgb(28, 26, 42)
	colBorder      = rgb(67, 57, 88)
	colText        = rgb(247, 243, 251)
	colSecondary   = rgb(181, 173, 194)
	colGold        = rgb(247, 184, 86)
	colPurple      = rgb(118, 66, 164)
	colPurpleHover = rgb(140, 85, 188)
	colMuted       = rgb(55, 50, 69)
	colOff         = rgb(146, 138, 159)
	colGreen       = rgb(100, 210, 173)
)

func makeFont(height, weight int32) uintptr {
	// ClearType + Segoe UI, supplied by Windows (not bundled).
	v, _, _ := gdCreateFont.Call(uintptr(height), 0, 0, 0, uintptr(weight), 0, 0, 0,
		1, 0, 0, 5, 0, uintptr(unsafe.Pointer(toW("Segoe UI"))))
	return v
}
func newNativeTheme() *nativeTheme {
	bg, _, _ := gdCreateSolidBrush.Call(colBackground)
	card, _, _ := gdCreateSolidBrush.Call(colCard)
	detail, _, _ := gdCreateSolidBrush.Call(colDetail)
	muted, _, _ := gdCreateSolidBrush.Call(colMuted)
	return &nativeTheme{
		brushBackground: bg, brushCard: card, brushDetail: detail, brushMuted: muted,
		fontTitle: makeFont(-34, 700), fontSubtitle: makeFont(-16, 400),
		fontSection: makeFont(-14, 700), fontCard: makeFont(-26, 700),
		fontButton: makeFont(-17, 600), fontSmall: makeFont(-14, 400),
	}
}
func (t *nativeTheme) styleLabel(id string, hwnd uintptr) {
	font := t.fontSubtitle
	if id == "installed" || id == "official" || id == "grim" {
		font = t.fontCard
	}
	if id == "status" {
		font = t.fontSmall
	}
	procSendMessageW.Call(hwnd, wmSetFont, font, 1)
}
func (t *nativeTheme) styleButton(hwnd uintptr) {
	procSendMessageW.Call(hwnd, wmSetFont, t.fontButton, 1)
}
func (t *nativeTheme) colorStatic(dc, control uintptr, labels map[string]uintptr) uintptr {
	brush := t.brushBackground
	text := colSecondary
	for id, hwnd := range labels {
		if hwnd != control {
			continue
		}
		switch id {
		case "installed", "official", "grim":
			brush = t.brushCard
			text = colText
		case "detail":
			brush = t.brushDetail
			text = colText
		case "status":
			brush = t.brushDetail
			text = colSecondary
		}
		break
	}
	gdSetTextColor.Call(dc, text)
	gdSetBkMode.Call(dc, 1) // TRANSPARENT
	return brush
}
func shape(dc uintptr, x, y, w, h, rad int32, fill, edge uintptr) {
	brush, _, _ := gdCreateSolidBrush.Call(fill)
	pen, _, _ := gdCreatePen.Call(0, 1, edge)
	oldBrush, _, _ := gdSelectObject.Call(dc, brush)
	oldPen, _, _ := gdSelectObject.Call(dc, pen)
	gdRoundRect.Call(dc, uintptr(x), uintptr(y), uintptr(x+w), uintptr(y+h), uintptr(rad), uintptr(rad))
	gdSelectObject.Call(dc, oldPen)
	gdSelectObject.Call(dc, oldBrush)
	gdDeleteObject.Call(pen)
	gdDeleteObject.Call(brush)
}
func fillRect(dc uintptr, r nativeRect, brush uintptr) {
	gdFillRect.Call(dc, uintptr(unsafe.Pointer(&r)), brush)
}
func textAt(dc uintptr, text string, x, y, w, h int32, font, color uintptr, flags uint32) {
	if text == "" {
		return
	}
	old, _, _ := gdSelectObject.Call(dc, font)
	gdSetBkMode.Call(dc, 1)
	gdSetTextColor.Call(dc, color)
	r := nativeRect{x, y, x + w, y + h}
	gdDrawText.Call(dc, uintptr(unsafe.Pointer(toW(text))), uintptr(^uint32(0)), uintptr(unsafe.Pointer(&r)), uintptr(flags))
	gdSelectObject.Call(dc, old)
}

const (
	dtLeft        = 0
	dtCenter      = 1
	dtVCenter     = 4
	dtSingle      = 0x20
	dtEndEllipsis = 0x8000
)

func (t *nativeTheme) paint(hwnd uintptr) {
	var ps paintStruct
	dc, _, _ := gdBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	if dc == 0 {
		return
	}
	defer gdEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	var client nativeRect
	gdGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client)))
	fillRect(dc, client, t.brushBackground)

	// Compact composition: reserved space between all cards, headings and
	// controls, including on Windows where the caption reduces client height.
	headerBrush, _, _ := gdCreateSolidBrush.Call(colHeader)
	fillRect(dc, nativeRect{0, 0, client.Right, 110}, headerBrush)
	gdDeleteObject.Call(headerBrush)
	goldBrush, _, _ := gdCreateSolidBrush.Call(colGold)
	fillRect(dc, nativeRect{0, 0, client.Right, 3}, goldBrush)
	gdDeleteObject.Call(goldBrush)

	if t.logo == 0 {
		instance, _, _ := procGetModuleHandleW.Call(0)
		t.logo, _, _ = procLoadImageW.Call(instance, 1, 1, 69, 69, 0)
	}
	if t.logo != 0 {
		gdDrawIconEx.Call(dc, 30, 22, t.logo, 69, 69, 0, 0, 3)
	}
	textAt(dc, "GRIM POSTER", 112, 20, 495, 47, t.fontTitle, colGold, dtLeft|dtSingle)
	textAt(dc, "Blood on the Clocktower  /  Companion Manager", 114, 67, 530, 24, t.fontSubtitle, colSecondary, dtLeft|dtSingle)
	shape(dc, 680, 28, 108, 32, 13, colCard, colBorder)
	textAt(dc, "v"+bootstrapVersion, 683, 33, 102, 22, t.fontSmall, colText, dtCenter|dtVCenter|dtSingle)

	textAt(dc, "YOUR SETUP", 30, 123, 215, 22, t.fontSection, colSecondary, dtLeft|dtSingle)
	// Three consistently spaced version cards.
	shape(dc, 30, 154, 239, 101, 16, colCard, colBorder)
	shape(dc, 293, 154, 239, 101, 16, colCard, colBorder)
	shape(dc, 556, 154, 233, 101, 16, colCard, colBorder)
	textAt(dc, "BOTC INSTALLED", 48, 171, 199, 20, t.fontSection, colSecondary, dtLeft|dtSingle)
	textAt(dc, "OFFICIAL LATEST", 311, 171, 199, 20, t.fontSection, colSecondary, dtLeft|dtSingle)
	textAt(dc, "SIGNED GRIM POSTER", 574, 171, 199, 20, t.fontSection, colSecondary, dtLeft|dtSingle)
	shape(dc, 48, 243, 42, 3, 3, colGreen, colGreen)
	shape(dc, 311, 243, 42, 3, 3, colGold, colGold)
	shape(dc, 574, 243, 42, 3, 3, colPurpleHover, colPurpleHover)

	textAt(dc, "STATUS", 30, 267, 160, 19, t.fontSection, colSecondary, dtLeft|dtSingle)
	shape(dc, 30, 290, 759, 63, 14, colDetail, colBorder)
	shape(dc, 34, 303, 4, 35, 4, colPurpleHover, colPurpleHover)

	textAt(dc, "QUICK ACTIONS", 30, 364, 220, 19, t.fontSection, colSecondary, dtLeft|dtSingle)
	// Owner-drawn buttons occupy y=385..554; headings never overlap them.

	shape(dc, 30, 561, 759, 49, 12, colDetail, colBorder)
	shape(dc, 30, 623, 759, 54, 12, colDetail, colBorder)
	textAt(dc, "Updates use the official GitHub and the latest verified Grim Poster feed.", 30, 691, 759, 18, t.fontSmall, colSecondary, dtLeft|dtSingle|dtEndEllipsis)

}
func (t *nativeTheme) drawButton(item uintptr) {
	if item == 0 {
		return
	}
	dis := (*drawItem)(unsafe.Pointer(item))
	dc := dis.DC
	id := int(dis.CtlID)
	disabled := dis.State&4 != 0
	selected := dis.State&1 != 0
	fill, edge, text := colCard, colBorder, colText
	if id == idLaunch {
		fill = colPurple
		edge = colPurpleHover
	}
	if id == idInstallGrim {
		fill = rgb(61, 42, 83)
		edge = rgb(126, 87, 163)
	}
	if id == idInstallBotc {
		fill = rgb(44, 38, 65)
		edge = colBorder
	}
	if id == idRefresh || id == idOfficial || id == idLogs || id == idSendReport || id == idMode {
		fill = colDetail
		edge = colBorder
		text = colSecondary
	}
	if id == idUninstall {
		fill = rgb(63, 35, 47)
		edge = rgb(150, 75, 85)
		text = rgb(255, 211, 215)
	}
	if selected && !disabled {
		fill = colPurpleHover
	}
	if disabled {
		fill = rgb(37, 34, 48)
		edge = colMuted
		text = colOff
	}
	w := dis.Rect.Right - dis.Rect.Left
	h := dis.Rect.Bottom - dis.Rect.Top
	// Erase the entire owner-drawn surface first. Without this, Windows theme
	// pixels at rounded corners appear as white triangles on dark buttons.
	fillRect(dc, nativeRect{0, 0, w, h}, t.brushBackground)
	shape(dc, 1, 1, w-2, h-2, 14, fill, edge)
	size, _, _ := gdGetWindowTextLength.Call(dis.Hwnd)
	buf := make([]uint16, int(size)+2)
	gdGetWindowText.Call(dis.Hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	label := syscall.UTF16ToString(buf)
	font := t.fontButton
	if id == idLaunch {
		font = makeFont(-19, 700)
		defer gdDeleteObject.Call(font)
	}
	textAt(dc, label, 10, 1, w-20, h-2, font, text, dtCenter|dtVCenter|dtSingle|dtEndEllipsis)
	if dis.State&0x10 != 0 { // ODS_FOCUS: retain keyboard focus indication.
		r := nativeRect{5, 5, w - 6, h - 6}
		gdDrawFocusRect.Call(dc, uintptr(unsafe.Pointer(&r)))
	}
}
