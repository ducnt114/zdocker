package ui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Test harness, adapted from kboba: drive the root model like the Bubble
// Tea runtime would, synchronously.

// send runs msg through Update, then executes the returned commands and
// feeds their messages back in, recursively. Commands that don't finish
// quickly (cursor blink, ticks, blocking waits) are abandoned.
func send(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	return sendDepth(m, msg, 0)
}

func sendDepth(m Model, msg tea.Msg, depth int) Model {
	next, cmd := m.Update(msg)
	m = next.(Model)
	if depth >= 5 {
		return m
	}
	for _, out := range runCmd(cmd) {
		m = sendDepth(m, out, depth+1)
	}
	return m
}

// runCmd executes cmd and returns the resulting messages, expanding
// tea.Batch; a command still running after a short timeout is abandoned.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(50 * time.Millisecond):
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, runCmd(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

func press(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// typeCommand simulates typing ":<cmd>" followed by enter.
func typeCommand(t *testing.T, m Model, cmd string) Model {
	t.Helper()
	m = send(t, m, press(":"))
	for _, r := range cmd {
		m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return send(t, m, press("enter"))
}
