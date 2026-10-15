package ui

import "github.com/charmbracelet/bubbles/key"

// globalKeys are available in every view (unless a text input has focus).
type globalKeys struct {
	Command key.Binding
	Help    key.Binding
	Back    key.Binding
	Quit    key.Binding
}

var globalKeyMap = globalKeys{
	Command: key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "command")),
	Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Back:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
}

// helpKeys combines a view's own bindings with the global ones so it can be
// rendered by bubbles/help. It implements help.KeyMap.
type helpKeys struct {
	view []key.Binding
}

func (h helpKeys) ShortHelp() []key.Binding {
	g := globalKeyMap
	return append(append([]key.Binding{}, h.view...), g.Command, g.Help, g.Quit)
}

func (h helpKeys) FullHelp() [][]key.Binding {
	g := globalKeyMap
	return [][]key.Binding{
		h.view,
		{g.Command, g.Help, g.Quit},
		{
			key.NewBinding(key.WithKeys(":containers"), key.WithHelp(":containers", "containers (:ps)")),
			key.NewBinding(key.WithKeys(":images"), key.WithHelp(":images", "images (:img)")),
			key.NewBinding(key.WithKeys(":q"), key.WithHelp(":q", "quit")),
		},
	}
}
