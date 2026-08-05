# SSH - Remote

Micro can work against a remote machine over SSH, VS Code Remote-SSH style:
the Explorer browses the remote filesystem, Docker manages the remote
daemon, and you get a real interactive remote shell in the terminal panel -
all through your existing SSH setup (config aliases, agent, keys). Micro
never handles credentials itself; everything shells out to your system's
`ssh` binary.

## Connecting: the SSH wizard

Press `Alt-4` (or `Ctrl-Shift-4`, or `F8`, or run `> ssh`) to open the SSH wizard. When you aren't connected to
anything, it walks you through two quick steps:

1. **Pick a host** - a popup lists every `Host` alias from your
   `~/.ssh/config`, plus a "Custom target..." option if you want to type
   one directly (`user@host`, `host`, anything you'd pass to `ssh`).
   Use `Up`/`Down`/`Enter`, or click an entry with the mouse. `Esc`
   cancels.
2. **Pick a remote path** - leave it blank for the remote user's home
   directory, or type an absolute (or `~`-relative) path.

While connecting (and while the Docker view is loading data from a remote
host), you'll see a small animated spinner in the info bar, e.g. `⠋
Connecting to myserver...` - connecting never blocks the rest of the
editor, so you can keep typing/browsing while it works in the background.

Once resolved, this:
* Switches the Explorer to browse that remote directory
* Points the Docker view at the remote Docker daemon (via `docker -H
  ssh://target`, the Docker CLI's own SSH transport)
* Opens a real interactive remote shell in a new terminal panel tab

You can also connect directly from the command line: `> ssh <target>
[remote-path]`.

## Alt-4 / Ctrl-Shift-4 / F8 while already connected

Press `Alt-4` (or `Ctrl-Shift-4`, or `F8`, or run `> ssh`) again while connected and you'll get a popup instead:

* **Disconnect from `<target>`** - closes the current session and falls
  back to whatever you were connected to before (or to local, if this was
  your only session).
* **Connect to another host (nested)** - runs the same host/path wizard
  and stacks the new session on top of the current one, without losing it.
  This is handy for jumping to a second machine reachable from the first
  (or just for keeping several remote contexts around at once) - the
  Explorer/Docker titles show `(target +N)` when N sessions are nested
  underneath the active one.
* **Disconnect all (back to local)** - only shown once you have a nested
  session - drops the whole stack at once.

`> ssh` with no arguments opens this same wizard/menu from the command
line.

## Editing remote files

Opening a file from the remote Explorer downloads it to a local mirror file
under your temp directory and opens that mirror as an entirely normal
buffer - undo, backups, sudo-save, etc. all work exactly as they do
locally. Saving that buffer (`Ctrl-s`, autosave, ...) automatically pushes
the new contents back to the remote file over SSH; you'll see a "Pushed to
target:path" message in the info bar once it lands.

## Limitations

* Directory listings use `find`/GNU `ls` conventions and assume a
  POSIX-like remote shell (Linux/macOS/WSL); this is not tested against
  particularly unusual remote setups.
* "Nested" connections are independent SSH sessions from your local
  machine to each host (not a chained/ProxyJump hop through the first
  host) - each just gets pushed on top of a stack so disconnecting steps
  back through them in order.
* There is no remote text search across the whole tree (only per-open-file
  search, same as any buffer).
* Large binary files are downloaded/uploaded whole - there is no partial
  sync.
