// =============================================================================
// File: internal/theme/theme.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package theme defines the editor's curated color palette. The editor
// intentionally ships one opinionated dark theme — there is no runtime
// configuration, no theme file, no JSON. To restyle the editor, edit this
// file and recompile. The palette is inspired by Tokyo Night and tuned so
// the syntax colors stay legible against the chrome.
package theme

import (
	"os"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// Theme bundles every color the editor renders. UI surfaces, accents, and
// syntax-highlight colors all live in one struct so that adjusting one
// element of the palette can be balanced against the others.
type Theme struct {
	// --- Surfaces ---
	BG        tcell.Color // Editor background.
	SidebarBG tcell.Color // File tree / inactive tab background, slightly darker than BG.
	StatusBG  tcell.Color // Status bar background.
	LineHL    tcell.Color // Active line highlight.

	// --- Foregrounds & accents ---
	Text        tcell.Color // Primary editor text.
	Muted       tcell.Color // Line numbers, inactive tabs, secondary UI text.
	Subtle      tcell.Color // Even more subtle (separators, hints).
	Accent      tcell.Color // Active tab accent, root label, important UI.
	AccentSoft  tcell.Color // Softer accent (active line number).
	Selection   tcell.Color // Selection background.
	Modified    tcell.Color // Dirty indicator (unsaved changes).
	Error       tcell.Color // Error messages.
	GitModified tcell.Color
	GitAdded    tcell.Color
	GitDeleted  tcell.Color
	GitRenamed  tcell.Color
	GitMixed    tcell.Color

	// FindMatch / FindCurrent paint search hits in the editor body.
	// FindMatch is a soft tint applied to every match in the viewport;
	// FindCurrent is the louder color drawn under the "active" match
	// (the one Enter/Esc-g will jump past) so the user can find their
	// place at a glance.
	FindMatch   tcell.Color
	FindCurrent tcell.Color

	// ConflictOurs / ConflictBase / ConflictTheirs tint the body of an
	// unresolved merge conflict. They are BACKGROUNDS, painted under the
	// existing syntax foreground, so all three have to stay dark enough for
	// Text to read on top. Three rather than two because git's diff3 conflict
	// style adds a common-ancestor section between ||||||| and =======, and a
	// section drawn in one of the other two colours would look like content
	// someone chose.
	ConflictOurs   tcell.Color
	ConflictBase   tcell.Color
	ConflictTheirs tcell.Color

	// --- File tree ---
	FolderColor tcell.Color
	FileColor   tcell.Color

	// --- Syntax highlighting ---
	SynKeyword  tcell.Color
	SynString   tcell.Color
	SynNumber   tcell.Color
	SynComment  tcell.Color
	SynFunction tcell.Color
	SynType     tcell.Color
	SynBuiltin  tcell.Color
	SynVariable tcell.Color
	SynOperator tcell.Color
	SynPunct    tcell.Color
	SynConstant tcell.Color
}

// Default mirrors OMP's titanium-paul palette. Explorr intentionally keeps
// its single compile-time theme in the fork rather than loading runtime files.
func Default() Theme {
	return Theme{
		BG:        tcell.NewRGBColor(0x15, 0x18, 0x20),
		SidebarBG: tcell.NewRGBColor(0x0f, 0x12, 0x16),
		StatusBG:  tcell.NewRGBColor(0x0f, 0x12, 0x16),
		LineHL:    tcell.NewRGBColor(0x1f, 0x25, 0x2d),

		Text:        tcell.NewRGBColor(0xe8, 0xec, 0xf4),
		Muted:       tcell.NewRGBColor(0x9c, 0xa3, 0xb0),
		Subtle:      tcell.NewRGBColor(0x2a, 0x30, 0x38),
		Accent:      tcell.NewRGBColor(0x00, 0xb4, 0xff),
		AccentSoft:  tcell.NewRGBColor(0xd4, 0xc0, 0x90),
		Selection:   tcell.NewRGBColor(0x00, 0x82, 0xb3),
		Modified:    tcell.NewRGBColor(0xff, 0xb3, 0x47),
		Error:       tcell.NewRGBColor(0xff, 0x47, 0x57),
		GitModified: tcell.NewRGBColor(0xff, 0xb3, 0x47),
		GitAdded:    tcell.NewRGBColor(0x00, 0xff, 0x88),
		GitDeleted:  tcell.NewRGBColor(0xff, 0x47, 0x57),
		GitRenamed:  tcell.NewRGBColor(0x00, 0xb4, 0xff),
		GitMixed:    tcell.NewRGBColor(0xd4, 0xc0, 0x90),

		FindMatch:   tcell.NewRGBColor(0x3e, 0x44, 0x51),
		FindCurrent: tcell.NewRGBColor(0x00, 0x82, 0xb3),

		ConflictOurs:   tcell.NewRGBColor(0x0f, 0x2b, 0x22),
		ConflictBase:   tcell.NewRGBColor(0x2b, 0x31, 0x3b),
		ConflictTheirs: tcell.NewRGBColor(0x10, 0x27, 0x36),

		FolderColor: tcell.NewRGBColor(0x00, 0xb4, 0xff),
		FileColor:   tcell.NewRGBColor(0xe8, 0xec, 0xf4),

		SynKeyword:  tcell.NewRGBColor(0x00, 0xb4, 0xff),
		SynString:   tcell.NewRGBColor(0xd4, 0xc0, 0x90),
		SynNumber:   tcell.NewRGBColor(0xff, 0xb3, 0x47),
		SynComment:  tcell.NewRGBColor(0x6b, 0x72, 0x80),
		SynFunction: tcell.NewRGBColor(0x00, 0xff, 0x88),
		SynType:     tcell.NewRGBColor(0x00, 0xb4, 0xff),
		SynBuiltin:  tcell.NewRGBColor(0xff, 0x47, 0x57),
		SynVariable: tcell.NewRGBColor(0xe8, 0xec, 0xf4),
		SynOperator: tcell.NewRGBColor(0x00, 0xb4, 0xff),
		SynPunct:    tcell.NewRGBColor(0x9c, 0xa3, 0xb0),
		SynConstant: tcell.NewRGBColor(0xff, 0xb3, 0x47),
	}
}

// FromHerdR overlays the active HerdR palette exported to workspace plugin
// panes. Unknown or missing values keep the editor's built-in palette.
func FromHerdR(base Theme) Theme {
	apply := func(target *tcell.Color, key string) {
		if value, ok := os.LookupEnv(key); ok {
			if color, valid := parseHerdRColor(value); valid {
				*target = color
			}
		}
	}

	apply(&base.BG, "HERDR_THEME_PANEL_BG")
	apply(&base.SidebarBG, "HERDR_THEME_SIDEBAR_BG")
	apply(&base.StatusBG, "HERDR_THEME_SIDEBAR_BG")
	apply(&base.LineHL, "HERDR_THEME_SURFACE0")
	apply(&base.Text, "HERDR_THEME_TEXT")
	apply(&base.Muted, "HERDR_THEME_OVERLAY0")
	apply(&base.Subtle, "HERDR_THEME_SURFACE_DIM")
	apply(&base.Accent, "HERDR_THEME_ACCENT")
	apply(&base.AccentSoft, "HERDR_THEME_OVERLAY1")
	apply(&base.Selection, "HERDR_THEME_SURFACE1")
	apply(&base.Modified, "HERDR_THEME_YELLOW")
	apply(&base.Error, "HERDR_THEME_RED")
	apply(&base.GitModified, "HERDR_THEME_YELLOW")
	apply(&base.GitAdded, "HERDR_THEME_GREEN")
	apply(&base.GitDeleted, "HERDR_THEME_RED")
	apply(&base.GitRenamed, "HERDR_THEME_BLUE")
	apply(&base.GitMixed, "HERDR_THEME_MAUVE")
	apply(&base.FolderColor, "HERDR_THEME_ACCENT")
	apply(&base.FileColor, "HERDR_THEME_TEXT")
	return base
}

// parseHerdRColor decodes the stable palette strings exported by HerdR.
func parseHerdRColor(value string) (tcell.Color, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "reset" {
		return tcell.ColorDefault, true
	}
	if hex := strings.TrimPrefix(value, "#"); len(hex) == 6 && len(value) == 7 {
		rgb, err := strconv.ParseUint(hex, 16, 24)
		if err == nil {
			return tcell.NewRGBColor(int32(rgb>>16), int32(rgb>>8&0xff), int32(rgb&0xff)), true
		}
	}
	if index, ok := strings.CutPrefix(value, "indexed:"); ok {
		parsed, err := strconv.ParseUint(index, 10, 8)
		if err == nil {
			return tcell.PaletteColor(int(parsed)), true
		}
	}

	switch value {
	case "black":
		return tcell.ColorBlack, true
	case "red":
		return tcell.ColorMaroon, true
	case "green":
		return tcell.ColorGreen, true
	case "yellow":
		return tcell.ColorOlive, true
	case "blue":
		return tcell.ColorNavy, true
	case "magenta":
		return tcell.ColorPurple, true
	case "cyan":
		return tcell.ColorTeal, true
	case "gray":
		return tcell.ColorSilver, true
	case "darkgray":
		return tcell.ColorGray, true
	case "lightred":
		return tcell.ColorRed, true
	case "lightgreen":
		return tcell.ColorLime, true
	case "lightyellow":
		return tcell.ColorYellow, true
	case "lightblue":
		return tcell.ColorBlue, true
	case "lightmagenta":
		return tcell.ColorFuchsia, true
	case "lightcyan":
		return tcell.ColorAqua, true
	case "white":
		return tcell.ColorWhite, true
	default:
		return tcell.ColorDefault, false
	}
}
