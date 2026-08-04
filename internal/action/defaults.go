package action

var termdefaults = map[string]string{
	"<Ctrl-q><Ctrl-q>": "Exit",
	"<Ctrl-e><Ctrl-e>": "CommandMode",
	"<Ctrl-w><Ctrl-w>": "NextSplit|FirstSplit",
}

// sidebardefaults and termpaneldefaults are declared for documentation and
// future JSON-configurability; the sidebar and terminal panel currently
// handle their own key dispatch directly (see sidebar.go/termpanel.go)
// rather than going through the generic Binder/KeyTree machinery.
var sidebardefaults = map[string]string{}
var termpaneldefaults = map[string]string{}

// DefaultBindings returns a map containing micro's default keybindings
func DefaultBindings(pane string) map[string]string {
	switch pane {
	case "command":
		return infodefaults
	case "buffer":
		return bufdefaults
	case "terminal":
		return termdefaults
	case "sidebar":
		return sidebardefaults
	case "termpanel":
		return termpaneldefaults
	default:
		return map[string]string{}
	}
}
