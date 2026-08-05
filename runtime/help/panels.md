# Explorer, Docker and the terminal panel

Micro has three built-in IDE-style panels: a file **Explorer**, a **Docker**
management view (sharing the left sidebar with Explorer), and a bottom
**terminal panel** supporting multiple named terminal tabs. All three are
fully usable with the mouse as well as the keyboard.

The Explorer and Docker view both work against a remote machine over SSH
too - see `> help ssh`. When connected, their titles show `(target)` so
it's always clear whether you're looking at local or remote state.

## Recently viewed files

Above the editor area, a row of closable file tabs (like VS Code's editor
tabs) tracks every file you've opened via the Explorer, `> open`, or
`Ctrl-o`. Click a tab to switch to it, or click its `×` to close it.

Switching tabs never loses your place: each tab keeps its own buffer alive
(with unsaved changes, undo history, cursor position and scroll offset
intact) even while it's hidden behind another tab, so coming back to a
file puts you right back on the line you left it on. A `●` before a tab's
name means it has unsaved changes.

## Toggling the panels

* `Alt-1` (or `Ctrl-Shift-1`): toggle the Explorer (left sidebar)
* `Alt-2` (or `Ctrl-Shift-2`): toggle the Docker view (left sidebar)
* `Alt-3` (or `Ctrl-Shift-3`): toggle the terminal panel (bottom)
* `Alt-4` (or `Ctrl-Shift-4`): open the SSH connect/disconnect wizard (see `> help ssh`)
* `Alt-5` (or `Ctrl-Shift-5`): change the working directory / open a local folder

These keybindings work no matter which part of the editor currently has
focus. You can also use the commands `> explorer`, `> docker` and
`> termpanel`.

If Alt- doesn't seem to do anything - common on macOS, where Option only
sends a real Alt/Meta modifier if you've turned that on in your terminal's
settings, and Cmd is never forwarded to any terminal app at all - try the
Ctrl-Shift- version instead. If that doesn't work either (some terminals
don't report it as a distinct combo), `Ctrl-e` (command mode) then
`explorer`/`docker`/`termpanel` + Enter always works; see
`> help defaultkeys` for the full explanation.

The left sidebar has a small activity strip on its far left edge (an `E` and
a `D`) that you can click to switch between the Explorer and Docker views,
VS Code-style.

Both the sidebar and the terminal panel can be resized by dragging: click
and drag the sidebar's right border, or click and drag an empty area of the
terminal panel's tab strip (e.g. past the `+` button), and release.

## Explorer

The Explorer shows a file tree rooted at micro's working directory.

* Click a folder, or press `Enter`/`Right`/`Left` on it, to expand/collapse it
* Click (or double-click) a file, or press `Enter` on it, to open it in the
  active editor pane
* `Up`/`Down`/`j`/`k`: move the selection
* `n`: create a new file in the selected directory
* `N`: create a new folder in the selected directory
* `R`: rename the selected file/folder
* `d`: delete the selected file/folder (with confirmation)
* `r`: refresh the tree
* `Esc`: move focus back to the editor

A fixed `.. (up a folder)` row appears above the tree whenever the current
root has a parent directory. Click it, or select it and press `Enter`, to
re-root the Explorer one level up.

Right-click a folder (or select it and press `m`) to open a small context
menu with **Open folder here** (re-roots the Explorer - and, if local,
micro's working directory - at that folder) and **Cancel**.

### Changing the working directory

Press `Alt-5`/`Ctrl-Shift-5` (or run `> openfolder`) to switch the Explorer to a
different local folder - the equivalent of VS Code's "Open Folder". It
prompts for a path (prefilled with the current directory, Tab-completes
like any file path) and, once you confirm, changes micro's working
directory and re-roots the Explorer there. If you were connected to a
remote host, it disconnects first, since browsing a local folder while
Docker/terminal are still pointed at a remote host would be confusing.

## Docker

The Docker view lists containers (grouped by compose project when
applicable), images, networks and volumes. It requires the `docker` CLI to
be installed and the daemon to be reachable; otherwise it shows a short
message explaining why it's unavailable.

* Click, or press `Enter`, on a category/compose group to expand/collapse it
* Right-click a row (or press `m` on the selected row) to open a context
  menu with the actions available for that row
* `Up`/`Down`/`j`/`k`: move the selection
* `r`: refresh
* With a container selected:
  * `s`: stop
  * `S`: start
  * `x`: restart
  * `d`: remove (with confirmation)
  * `l`: stream logs into a new terminal panel tab
  * `e`: exec an interactive shell into a running container, in a new
    terminal panel tab
  * `i`: inspect (opens the pretty-printed JSON in a new tab)
* With an image/network/volume selected: `d` removes it, `i` inspects it
* With a category selected: `p` prunes that category (stopped containers,
  dangling images, unused networks, unused volumes)

## Terminal panel

The terminal panel is a bottom panel, independent of your editor tabs and
splits, that can hold any number of terminal tabs.

* Click a tab to switch to it, click its `x` to close it, or click `+` to
  open a new one
* `Ctrl-t Ctrl-t` (press `Ctrl-t` twice in a row): open a new terminal tab
* `Ctrl-w Ctrl-w`: close the current terminal tab
* `Ctrl-n Ctrl-n`: rename the current terminal tab
* `Ctrl-o Ctrl-o`: move focus back to the editor, leaving the terminal panel open
* `Ctrl-Left`/`Ctrl-Right`: switch to the previous/next terminal tab

Everything else you type is sent straight to the shell running in the
active terminal tab, exactly like micro's existing `> term` command.
