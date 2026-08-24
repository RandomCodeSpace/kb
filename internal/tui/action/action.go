// Package action owns kb's keyboard action registry: one table naming every
// action the board offers, the key that drives it, and the words that describe
// it.
//
// The table exists so that two surfaces cannot disagree about the keymap. The ?
// help pane of spec section 5.2 reads it to build its bindings, and the ctrl+k
// command palette reads it to build its entries. Neither spells a key or a
// description of its own, so a rebind or a reword is one edit here and both
// surfaces move together. A surface that needs an action the table does not
// carry has found a missing row, not a reason for a literal.
//
// Everything here is a pure function over immutable data: no styles, no
// bubbletea state, no store. The one dependency is bubbletea, and only so that
// a row can hand back the key press it stands for.
package action

import (
	tea "charm.land/bubbletea/v2"
)

// ID is the stable identity of one action, independent of the key currently
// bound to it and of the words currently describing it.
type ID uint8

// The registry's rows. Order is the order the help pane and the palette list
// them in.
const (
	OpenCard ID = iota
	LiftCard
	SelectCard
	SelectColumn
	JumpColumn
	FilterText
	FilterLabel
	FilterClear
	SwitchProject
	OpenPalette

	ShipCard
	CancelCard
	RestoreCard
	PurgeCard
	NewCard
	EditCard
	OpenSettings
	SplitADR
	ImportIssue

	CloseHelp
	Quit
)

// Group is one column of the help pane and one section band of the palette.
type Group uint8

// The three groups. Navigate and Act are the help pane's two columns and the
// palette's two sections; Dismiss is the help pane's short-help ladder and is
// not offered by the palette, because closing the help pane is not something a
// palette entry can usefully do.
const (
	Navigate Group = iota
	Act
	Dismiss
)

// Label is the group's section band title in the palette.
func (g Group) Label() string {
	switch g {
	case Act:
		return "ACTIONS"
	case Dismiss:
		return "SESSION"
	case Navigate:
		return "NAVIGATE"
	default:
		return "NAVIGATE"
	}
}

// Feature is the board capability an action needs before it is offered. A board
// built without a card editor still carries the n and e rows; they are reported
// disabled rather than omitted, which is the self-managing-keymap property the
// help pane already relies on.
type Feature uint8

// The capabilities the registry gates on.
const (
	Always Feature = iota
	Editor
	Settings
	ADR
	Issues
)

// Features is what this board was built with, resolved once by the root model.
type Features struct {
	Editor   bool
	Settings bool
	ADR      bool
	Issues   bool
}

// Action is one row of the registry.
//
// Key is the key string the root model's own handler matches, which is why the
// palette can run an action by replaying it rather than by carrying a second
// copy of the dispatch. Hint is how that key is spelled to a reader and may
// name a pair the row stands for ("j/k"). Name is a lowercase verb phrase and
// is both the help description and the palette label, so the two can never
// drift.
type Action struct {
	ID    ID
	Group Group
	Need  Feature
	Key   string
	Hint  string
	Name  string
}

// registry is the table. Nothing else in kb spells a board keybinding.
var registry = []Action{
	{OpenCard, Navigate, Always, "enter", "enter", "open card"},
	{LiftCard, Navigate, Always, "space", "space", "lift or drop card"},
	{SelectCard, Navigate, Always, "j", "j/k", "select card"},
	{SelectColumn, Navigate, Always, "h", "h/l", "select column"},
	{JumpColumn, Navigate, Always, "1", "1-4", "jump to column"},
	{FilterText, Navigate, Always, "/", "/", "text filter"},
	{FilterLabel, Navigate, Always, "f", "f", "label filter"},
	{FilterClear, Navigate, Always, "X", "X", "clear filter"},
	{SwitchProject, Navigate, Always, "p", "p/P", "switch project"},
	{OpenPalette, Navigate, Always, PaletteKey, PaletteKey, "command palette"},

	{ShipCard, Act, Always, "t", "t", "ship card"},
	{CancelCard, Act, Always, "x", "x", "cancel card"},
	{RestoreCard, Act, Always, "r", "r", "restore card"},
	{PurgeCard, Act, Always, "D", "D", "permanently delete"},
	{NewCard, Act, Editor, "n", "n", "new card"},
	{EditCard, Act, Editor, "e", "e", "edit card"},
	{OpenSettings, Act, Settings, "s", "s", "settings"},
	{SplitADR, Act, ADR, "a", "a", "split ADR"},
	{ImportIssue, Act, Issues, "i", "i", "import forge issue"},

	{CloseHelp, Dismiss, Always, "?", "? or esc", "close help"},
	{Quit, Dismiss, Always, "q", "q", "quit"},
}

// PaletteKey is the chord that opens the command palette. It is named here
// rather than in the palette package because the registry is where every key
// kb binds is written down.
const PaletteKey = "ctrl+k"

// All is the whole registry in declaration order. The returned slice is a copy:
// the table is the durable artifact and no caller gets to edit it.
func All() []Action {
	return append([]Action(nil), registry...)
}

// InGroup is every row of one group, enabled or not, in declaration order. The
// help pane wants the disabled rows too, so it can render them dimmed rather
// than silently shrink a column.
func InGroup(group Group) []Action {
	out := make([]Action, 0, len(registry))
	for _, entry := range registry {
		if entry.Group == group {
			out = append(out, entry)
		}
	}
	return out
}

// Listed is every action the palette offers on this board: the enabled rows of
// the Navigate and Act groups, minus the row that opens the palette itself.
// Offering a palette entry that reopens the palette the user is already looking
// at is noise, and the Dismiss group's rows only make sense from the help pane.
func Listed(features Features) []Action {
	out := make([]Action, 0, len(registry))
	for _, entry := range registry {
		if entry.Group == Dismiss || entry.ID == OpenPalette {
			continue
		}
		if !entry.Enabled(features) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// Enabled reports whether the board was built with what this action needs.
func (a Action) Enabled(features Features) bool {
	switch a.Need {
	case Editor:
		return features.Editor
	case Settings:
		return features.Settings
	case ADR:
		return features.ADR
	case Issues:
		return features.Issues
	case Always:
		return true
	default:
		return true
	}
}

// KeyPress is the key press this action stands for, and reports false for a key
// the registry spells as a chord or a name the constructor does not cover.
//
// This is what lets the palette run an action without owning a second copy of
// the board's dispatch: it replays the press and the root model's own handler
// does the rest. It is also what feeds widget.Hotkey, which reads the press to
// decide where a label's underline lands.
func (a Action) KeyPress() (tea.KeyPressMsg, bool) {
	switch a.Key {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}, true
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace}, true
	}
	runes := []rune(a.Key)
	if len(runes) != 1 {
		return tea.KeyPressMsg{}, false
	}
	return tea.KeyPressMsg{Code: runes[0], Text: a.Key}, true
}
