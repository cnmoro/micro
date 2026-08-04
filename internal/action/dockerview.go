package action

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/micro-editor/micro/v2/internal/buffer"
	"github.com/micro-editor/micro/v2/internal/config"
	"github.com/micro-editor/micro/v2/internal/docker"
	"github.com/micro-editor/micro/v2/internal/screen"
	"github.com/micro-editor/micro/v2/internal/shell"
	"github.com/micro-editor/tcell/v2"
)

// dockerRowKind identifies what kind of row a dockerRow represents.
type dockerRowKind int

const (
	rowCategory dockerRowKind = iota
	rowComposeGroup
	rowContainer
	rowImage
	rowNetwork
	rowVolume
)

// dockerRow is a single flattened, displayable row in the Docker tree.
type dockerRow struct {
	kind  dockerRowKind
	depth int
	label string

	// identifiers used by actions; meaning depends on kind
	id   string
	name string

	container docker.Container
	image     docker.Image
	network   docker.Network
	volume    docker.Volume
}

// DockerView is the Docker-management sidebar view: containers (grouped by
// compose project), images, networks and volumes, with start/stop/restart/
// remove/logs/exec/inspect/prune actions.
type DockerView struct {
	available  bool
	unavailMsg string
	loaded     bool
	loading    bool

	containers []docker.Container
	images     []docker.Image
	networks   []docker.Network
	volumes    []docker.Volume
	compose    []docker.ComposeService

	collapsed map[string]bool // category/group keys collapsed by the user
	flat      []dockerRow

	selected int
	scroll   int
	// followSelection is set whenever the selection changes via the
	// keyboard, so Display scrolls it into view exactly once; mouse-wheel
	// scrolling never touches the selection, so it isn't affected.
	followSelection bool

	status string

	// context menu (opened via right-click or the 'm' key on a row)
	menuRow      int // index into flat this menu belongs to; -1 = closed
	menuItems    []dockerMenuItem
	menuSel      int
	menuStartRow int // local row (content-relative) the menu starts on
	menuWidth    int
}

// dockerMenuItem is a single action in a row's right-click context menu.
type dockerMenuItem struct {
	label string
	run   func()
}

func NewDockerView() *DockerView {
	d := new(DockerView)
	d.collapsed = map[string]bool{}
	d.menuRow = -1
	return d
}

func (d *DockerView) Title() string {
	if Remote != nil {
		return "DOCKER (" + Remote.Target + nestSuffix() + ")"
	}
	return "DOCKER"
}

// nestSuffix returns a short " +N" marker when there are nested SSH
// sessions underneath the active one, so it's clear you're several hops
// deep rather than directly connected.
func nestSuffix() string {
	if len(remoteStack) == 0 {
		return ""
	}
	return " +" + strconv.Itoa(len(remoteStack))
}

// Invalidate marks the currently loaded data as stale (e.g. because the
// active SSH target changed) without fetching anything or touching the
// info bar - the view will transparently refresh itself, showing its own
// spinner, next time it's actually displayed. Prefer this over Refresh()
// for "the world changed, catch up whenever" situations where an eager
// background fetch would just race a more important info bar message
// (e.g. "Connected to ..."/"Disconnected from ...").
func (d *DockerView) Invalidate() {
	d.loaded = false
}

// Refresh reloads the Docker view's data in the background (containers,
// compose projects, images, networks and volumes each mean a separate
// `docker` CLI round trip, which can take a noticeable moment over SSH),
// showing a spinner in the info bar and a "Loading..." placeholder in the
// view itself while it's in flight so the UI never appears to hang.
func (d *DockerView) Refresh() {
	d.closeMenu()
	d.loading = true

	label := "Loading Docker..."
	if Remote != nil {
		label = "Loading Docker (" + Remote.Target + ")..."
	}
	sp := StartSpinner(label)

	go func() {
		ok, msg := docker.Available()

		var containers []docker.Container
		var images []docker.Image
		var networks []docker.Network
		var volumes []docker.Volume
		var compose []docker.ComposeService
		var status string

		if ok {
			var err error
			if containers, err = docker.ListContainers(); err != nil {
				status = err.Error()
			}
			if images, err = docker.ListImages(); err != nil {
				status = err.Error()
			}
			if networks, err = docker.ListNetworks(); err != nil {
				status = err.Error()
			}
			if volumes, err = docker.ListVolumes(); err != nil {
				status = err.Error()
			}
			compose, _ = docker.ListCompose()
		}

		shell.Jobs <- shell.JobFunction{Function: func(string, []any) {
			d.loading = false
			d.available = ok
			d.unavailMsg = msg
			d.loaded = true
			d.status = status
			d.containers, d.images, d.networks, d.volumes, d.compose = containers, images, networks, volumes, compose
			d.rebuild()

			// Only speak up on error - errors are worth surfacing even if
			// it means clobbering a stale message, but on success the
			// view itself (the new container/image/... list) is already
			// the confirmation, and staying quiet means an automatic
			// refresh after e.g. connecting/disconnecting doesn't
			// clobber that more important message with a redundant one.
			// StopAndClear only removes the spinner's own last frame, so
			// it's safe even if something else already changed the info
			// bar message in the meantime.
			if !ok {
				sp.Stop()
				InfoBar.Error("Docker unavailable: " + msg)
			} else if status != "" {
				sp.Stop()
				InfoBar.Error(status)
			} else {
				sp.StopAndClear()
			}
		}}
	}()
}

func (d *DockerView) toggle(key string) {
	d.collapsed[key] = !d.collapsed[key]
}

func (d *DockerView) isCollapsed(key string) bool {
	return d.collapsed[key]
}

func (d *DockerView) rebuild() {
	d.flat = d.flat[:0]

	d.addContainers()
	d.addCategory("images", "Images", len(d.images), func() {
		sorted := append([]docker.Image(nil), d.images...)
		sort.Slice(sorted, func(i, j int) bool {
			return strings.ToLower(sorted[i].Repository) < strings.ToLower(sorted[j].Repository)
		})
		for _, img := range sorted {
			label := img.Repository + ":" + img.Tag + "  " + img.Size
			d.flat = append(d.flat, dockerRow{kind: rowImage, depth: 1, label: label, id: img.ID, name: img.Repository + ":" + img.Tag, image: img})
		}
	})
	d.addCategory("networks", "Networks", len(d.networks), func() {
		sorted := append([]docker.Network(nil), d.networks...)
		sort.Slice(sorted, func(i, j int) bool { return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name) })
		for _, n := range sorted {
			label := n.Name + "  (" + n.Driver + ")"
			d.flat = append(d.flat, dockerRow{kind: rowNetwork, depth: 1, label: label, id: n.ID, name: n.Name, network: n})
		}
	})
	d.addCategory("volumes", "Volumes", len(d.volumes), func() {
		sorted := append([]docker.Volume(nil), d.volumes...)
		sort.Slice(sorted, func(i, j int) bool { return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name) })
		for _, v := range sorted {
			d.flat = append(d.flat, dockerRow{kind: rowVolume, depth: 1, label: v.Name, id: v.Name, name: v.Name, volume: v})
		}
	})

	if d.selected >= len(d.flat) {
		d.selected = len(d.flat) - 1
	}
	if d.selected < 0 && len(d.flat) > 0 {
		d.selected = 0
	}
}

func (d *DockerView) addCategory(key, title string, count int, addChildren func()) {
	marker := "v"
	if d.isCollapsed(key) {
		marker = ">"
	}
	d.flat = append(d.flat, dockerRow{kind: rowCategory, depth: 0, label: fmt.Sprintf("%s %s (%d)", marker, title, count), id: key})
	if !d.isCollapsed(key) {
		addChildren()
	}
}

func (d *DockerView) addContainers() {
	byProject := map[string][]docker.Container{}
	var standalone []docker.Container
	for _, c := range d.containers {
		if p := c.ComposeProject(); p != "" {
			byProject[p] = append(byProject[p], c)
		} else {
			standalone = append(standalone, c)
		}
	}

	key := "containers"
	marker := "v"
	if d.isCollapsed(key) {
		marker = ">"
	}
	d.flat = append(d.flat, dockerRow{kind: rowCategory, depth: 0, label: fmt.Sprintf("%s Containers (%d)", marker, len(d.containers)), id: key})
	if d.isCollapsed(key) {
		return
	}

	projects := make([]string, 0, len(byProject))
	for p := range byProject {
		projects = append(projects, p)
	}
	sort.Strings(projects)

	for _, p := range projects {
		gkey := "compose:" + p
		gmarker := "v"
		if d.isCollapsed(gkey) {
			gmarker = ">"
		}
		d.flat = append(d.flat, dockerRow{kind: rowComposeGroup, depth: 1, label: fmt.Sprintf("%s %s (compose)", gmarker, p), id: gkey, name: p})
		if d.isCollapsed(gkey) {
			continue
		}
		cs := byProject[p]
		sort.Slice(cs, func(i, j int) bool { return cs[i].Names < cs[j].Names })
		for _, c := range cs {
			d.flat = append(d.flat, d.containerRow(c, 2))
		}
	}

	sort.Slice(standalone, func(i, j int) bool { return standalone[i].Names < standalone[j].Names })
	for _, c := range standalone {
		d.flat = append(d.flat, d.containerRow(c, 1))
	}
}

func (d *DockerView) containerRow(c docker.Container, depth int) dockerRow {
	icon := "o"
	if c.Running() {
		icon = "*"
	}
	label := icon + " " + c.Names + "  [" + c.Status + "]"
	return dockerRow{kind: rowContainer, depth: depth, label: label, id: c.ID, name: c.Names, container: c}
}

func (d *DockerView) clampSelection() {
	if d.selected < 0 {
		d.selected = 0
	}
	if d.selected >= len(d.flat) {
		d.selected = len(d.flat) - 1
	}
}

// scrollToSelected snaps scroll so the selected row is visible. Only call
// this right after the selection changed (e.g. via keyboard navigation) -
// calling it unconditionally from Display() would fight mouse-wheel
// scrolling, since a wheel scroll doesn't move the selection and would
// otherwise get snapped straight back to wherever the selection is.
func (d *DockerView) scrollToSelected(h int) {
	if d.selected < d.scroll {
		d.scroll = d.selected
	}
	if d.selected >= d.scroll+h {
		d.scroll = d.selected - h + 1
	}
	if d.scroll < 0 {
		d.scroll = 0
	}
}

// clampScroll keeps scroll within the valid range for the current content
// height, without regard to where the selection is. This is what Display
// calls every frame, so wheel-scrolling (which only changes scroll, not
// the selection) isn't undone on the next redraw.
func (d *DockerView) clampScroll(h int) {
	max := len(d.flat) - h
	if max < 0 {
		max = 0
	}
	if d.scroll > max {
		d.scroll = max
	}
	if d.scroll < 0 {
		d.scroll = 0
	}
}

func (d *DockerView) Display(x, y, w, h int) {
	if !d.loaded && !d.loading {
		d.Refresh()
	}

	style := config.DefStyle
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			screen.SetContent(x+col, y+row, ' ', nil, style)
		}
	}

	if d.loading && !d.loaded {
		drawWrapped(x, y, w, h, "Loading...", style)
		return
	}

	if !d.available {
		msg := "Docker unavailable: " + d.unavailMsg
		drawWrapped(x, y, w, h, msg, style)
		return
	}

	d.clampSelection()
	if d.followSelection {
		d.scrollToSelected(h)
		d.followSelection = false
	} else {
		d.clampScroll(h)
	}

	categoryStyle := style
	if s, ok := config.Colorscheme["constant"]; ok {
		categoryStyle = s
	}

	for row := 0; row < h; row++ {
		idx := d.scroll + row
		if idx < 0 || idx >= len(d.flat) {
			continue
		}
		r := d.flat[idx]

		st := style
		if r.kind == rowCategory || r.kind == rowComposeGroup {
			st = categoryStyle
		}
		if idx == d.selected {
			// Reverse (rather than swap in an unrelated style) so the
			// selection always has readable contrast against whatever
			// foreground/background this row already had, regardless of
			// the active colorscheme or terminal theme.
			st = st.Reverse(true)
		}

		col := r.depth * 2
		runes := []rune(r.label)
		for i := 0; i < len(runes) && col+i < w; i++ {
			screen.SetContent(x+col+i, y+row, runes[i], nil, st)
		}
		if idx == d.selected {
			for c := col + len(runes); c < w; c++ {
				screen.SetContent(x+c, y+row, ' ', nil, st)
			}
		}
	}

	d.displayMenu(x, y, w, h)
}

// displayMenu draws the right-click context menu, if one is open, as a
// small overlay directly below (or, if there's no room, above) the row it
// belongs to. It records the local row range/width it occupies so
// HandleClick can hit-test against it.
func (d *DockerView) displayMenu(x, y, w, h int) {
	if d.menuRow < 0 || d.menuRow < d.scroll || d.menuRow >= d.scroll+h {
		d.menuStartRow, d.menuWidth = -1, 0
		return
	}

	maxLabelLen := 0
	for _, it := range d.menuItems {
		if l := len([]rune(it.label)); l > maxLabelLen {
			maxLabelLen = l
		}
	}
	menuW := maxLabelLen + 2
	if menuW > w {
		menuW = w
	}

	anchorRow := d.menuRow - d.scroll
	startRow := anchorRow + 1
	if startRow+len(d.menuItems) > h {
		startRow = anchorRow - len(d.menuItems)
		if startRow < 0 {
			startRow = 0
		}
	}

	menuStyle := config.DefStyle
	if s, ok := config.Colorscheme["statusline"]; ok {
		menuStyle = s
	}

	for i, it := range d.menuItems {
		row := startRow + i
		if row < 0 || row >= h {
			continue
		}
		st := menuStyle
		if i == d.menuSel {
			st = st.Reverse(true)
		}
		label := " " + it.label
		runes := []rune(label)
		for c := 0; c < menuW; c++ {
			r := ' '
			if c < len(runes) {
				r = runes[c]
			}
			screen.SetContent(x+c, y+row, r, nil, st)
		}
	}

	d.menuStartRow, d.menuWidth = startRow, menuW
}

func drawWrapped(x, y, w, h int, msg string, style tcell.Style) {
	words := strings.Fields(msg)
	line := ""
	row := 0
	flush := func() {
		if row >= h {
			return
		}
		runes := []rune(line)
		for i := 0; i < len(runes) && i < w; i++ {
			screen.SetContent(x+i, y+row, runes[i], nil, style)
		}
		row++
		line = ""
	}
	for _, wd := range words {
		if len(line)+len(wd)+1 > w {
			flush()
		}
		if line != "" {
			line += " "
		}
		line += wd
	}
	if line != "" {
		flush()
	}
}

func (d *DockerView) rowAt(idx int) *dockerRow {
	if idx < 0 || idx >= len(d.flat) {
		return nil
	}
	return &d.flat[idx]
}

func (d *DockerView) toggleRow(idx int) {
	r := d.rowAt(idx)
	if r == nil {
		return
	}
	if r.kind == rowCategory || r.kind == rowComposeGroup {
		d.toggle(r.id)
		d.rebuild()
	}
}

func (d *DockerView) HandleClick(x, y int, button tcell.ButtonMask) {
	if !d.available {
		return
	}

	if button == tcell.ButtonSecondary {
		idx := d.scroll + y
		d.openMenu(idx)
		return
	}

	if d.menuRow >= 0 {
		if y >= d.menuStartRow && y < d.menuStartRow+len(d.menuItems) && x < d.menuWidth {
			item := d.menuItems[y-d.menuStartRow]
			d.closeMenu()
			item.run()
		} else {
			d.closeMenu()
		}
		return
	}

	idx := d.scroll + y
	if idx < 0 || idx >= len(d.flat) {
		return
	}
	d.selected = idx
	d.toggleRow(idx)
}

// openMenu opens the right-click/'m'-key context menu for the row at idx.
func (d *DockerView) openMenu(idx int) {
	items := d.buildMenuItems(idx)
	if len(items) == 0 {
		return
	}
	d.selected = idx
	d.menuRow = idx
	d.menuItems = items
	d.menuSel = 0
}

func (d *DockerView) closeMenu() {
	d.menuRow = -1
	d.menuItems = nil
	d.menuSel = 0
}

// buildMenuItems returns the context-menu actions available for the row at
// idx, depending on its kind.
func (d *DockerView) buildMenuItems(idx int) []dockerMenuItem {
	r := d.rowAt(idx)
	if r == nil {
		return nil
	}
	switch r.kind {
	case rowContainer:
		var items []dockerMenuItem
		if r.container.Running() {
			items = append(items,
				dockerMenuItem{"Stop", d.stopSelected},
				dockerMenuItem{"Restart", d.restartSelected},
				dockerMenuItem{"View logs", d.logsSelected},
				dockerMenuItem{"Exec shell", d.execSelected},
			)
		} else {
			items = append(items, dockerMenuItem{"Start", d.startSelected})
		}
		items = append(items,
			dockerMenuItem{"Inspect", d.inspectSelected},
			dockerMenuItem{"Remove", d.confirmRemove},
		)
		return items
	case rowImage, rowNetwork, rowVolume:
		return []dockerMenuItem{
			{"Inspect", d.inspectSelected},
			{"Remove", d.confirmRemove},
		}
	case rowCategory:
		items := []dockerMenuItem{{"Toggle expand/collapse", func() { d.toggleRow(idx) }}}
		switch r.id {
		case "containers", "images", "networks", "volumes":
			items = append(items, dockerMenuItem{"Prune", d.pruneSelected})
		}
		items = append(items, dockerMenuItem{"Refresh", d.Refresh})
		return items
	case rowComposeGroup:
		return []dockerMenuItem{{"Toggle expand/collapse", func() { d.toggleRow(idx) }}}
	}
	return nil
}

func (d *DockerView) HandleWheel(up bool) {
	if up {
		d.scroll -= 3
	} else {
		d.scroll += 3
	}
	if d.scroll < 0 {
		d.scroll = 0
	}
}

func (d *DockerView) HandleKey(e *tcell.EventKey) bool {
	if !d.available {
		if e.Key() == tcell.KeyRune && (e.Rune() == 'r' || e.Rune() == 'R') {
			d.Refresh()
			return true
		}
		return false
	}

	if d.menuRow >= 0 {
		switch e.Key() {
		case tcell.KeyUp:
			d.menuSel--
			if d.menuSel < 0 {
				d.menuSel = len(d.menuItems) - 1
			}
		case tcell.KeyDown:
			d.menuSel++
			if d.menuSel >= len(d.menuItems) {
				d.menuSel = 0
			}
		case tcell.KeyEnter:
			item := d.menuItems[d.menuSel]
			d.closeMenu()
			item.run()
		default:
			d.closeMenu()
		}
		return true
	}

	switch e.Key() {
	case tcell.KeyUp:
		d.selected--
		d.clampSelection()
		d.followSelection = true
		return true
	case tcell.KeyDown:
		d.selected++
		d.clampSelection()
		d.followSelection = true
		return true
	case tcell.KeyEnter:
		d.onEnter()
		return true
	case tcell.KeyRune:
		switch e.Rune() {
		case 'j':
			d.selected++
			d.clampSelection()
			d.followSelection = true
			return true
		case 'k':
			d.selected--
			d.clampSelection()
			d.followSelection = true
			return true
		case 'r', 'R':
			d.Refresh()
			return true
		case 'm', 'M':
			d.openMenu(d.selected)
			return true
		case 's':
			d.stopSelected()
			return true
		case 'S':
			d.startSelected()
			return true
		case 'x':
			d.restartSelected()
			return true
		case 'd':
			d.confirmRemove()
			return true
		case 'l':
			d.logsSelected()
			return true
		case 'e':
			d.execSelected()
			return true
		case 'i':
			d.inspectSelected()
			return true
		case 'p':
			d.pruneSelected()
			return true
		}
	}
	return false
}

func (d *DockerView) stopSelected() {
	d.withContainer(func(c docker.Container) {
		d.runAction("stop", c.Names, func() error { return docker.StopContainer(c.ID) })
	})
}

func (d *DockerView) startSelected() {
	d.withContainer(func(c docker.Container) {
		d.runAction("start", c.Names, func() error { return docker.StartContainer(c.ID) })
	})
}

func (d *DockerView) restartSelected() {
	d.withContainer(func(c docker.Container) {
		d.runAction("restart", c.Names, func() error { return docker.RestartContainer(c.ID) })
	})
}

func (d *DockerView) logsSelected() {
	d.withContainer(func(c docker.Container) {
		TermPanel.NewTabWithCommand("logs:"+c.Names, docker.LogsArgs(c.ID))
		TermPanel.Show()
	})
}

func (d *DockerView) execSelected() {
	d.withContainer(func(c docker.Container) {
		if !c.Running() {
			InfoBar.Error("Container is not running")
			return
		}
		TermPanel.NewTabWithCommand("exec:"+c.Names, docker.ExecArgs(c.ID))
		TermPanel.Show()
	})
}

func (d *DockerView) onEnter() {
	d.toggleRow(d.selected)
}

func (d *DockerView) withContainer(f func(docker.Container)) {
	r := d.rowAt(d.selected)
	if r == nil || r.kind != rowContainer {
		InfoBar.Error("Select a container first")
		return
	}
	f(r.container)
}

// runAction runs a docker CLI action in the background and reports the
// result (and refreshes the view) back on the main goroutine via
// shell.Jobs, the same mechanism plugin background jobs use, since
// DockerView's fields must only ever be mutated from the main event loop.
func (d *DockerView) runAction(verb, name string, action func() error) {
	sp := StartSpinner("Running docker " + verb + " " + name + "...")
	go func() {
		err := action()
		shell.Jobs <- shell.JobFunction{
			Function: func(out string, args []any) {
				sp.Stop()
				if err != nil {
					InfoBar.Error(err.Error())
				} else {
					InfoBar.Message("docker " + verb + " " + name + ": done")
				}
				d.Refresh()
			},
		}
	}()
}

func (d *DockerView) confirmRemove() {
	r := d.rowAt(d.selected)
	if r == nil {
		return
	}
	switch r.kind {
	case rowContainer:
		InfoBar.YNPrompt("Remove container "+r.name+"? (y,n,esc)", func(yes, canceled bool) {
			if canceled || !yes {
				return
			}
			d.runAction("rm", r.name, func() error { return docker.RemoveContainer(r.id, true) })
		})
	case rowImage:
		InfoBar.YNPrompt("Remove image "+r.name+"? (y,n,esc)", func(yes, canceled bool) {
			if canceled || !yes {
				return
			}
			d.runAction("rmi", r.name, func() error { return docker.RemoveImage(r.id, true) })
		})
	case rowNetwork:
		InfoBar.YNPrompt("Remove network "+r.name+"? (y,n,esc)", func(yes, canceled bool) {
			if canceled || !yes {
				return
			}
			d.runAction("network rm", r.name, func() error { return docker.RemoveNetwork(r.id) })
		})
	case rowVolume:
		InfoBar.YNPrompt("Remove volume "+r.name+"? (y,n,esc)", func(yes, canceled bool) {
			if canceled || !yes {
				return
			}
			d.runAction("volume rm", r.name, func() error { return docker.RemoveVolume(r.id, true) })
		})
	default:
		InfoBar.Error("Nothing removable selected")
	}
}

func (d *DockerView) pruneSelected() {
	r := d.rowAt(d.selected)
	if r == nil {
		return
	}
	var label string
	var action func() error
	switch {
	case r.id == "containers" || r.kind == rowContainer:
		label, action = "stopped containers", docker.PruneContainers
	case r.id == "images" || r.kind == rowImage:
		label, action = "dangling images", docker.PruneImages
	case r.id == "networks" || r.kind == rowNetwork:
		label, action = "unused networks", docker.PruneNetworks
	case r.id == "volumes" || r.kind == rowVolume:
		label, action = "unused volumes", docker.PruneVolumes
	default:
		InfoBar.Error("Select a category or item to prune")
		return
	}
	InfoBar.YNPrompt("Prune "+label+"? (y,n,esc)", func(yes, canceled bool) {
		if canceled || !yes {
			return
		}
		d.runAction("prune", label, action)
	})
}

func (d *DockerView) inspectSelected() {
	r := d.rowAt(d.selected)
	if r == nil {
		return
	}
	var kind string
	switch r.kind {
	case rowContainer:
		kind = "container"
	case rowImage:
		kind = "image"
	case rowNetwork:
		kind = "network"
	case rowVolume:
		kind = "volume"
	default:
		InfoBar.Error("Select an item to inspect")
		return
	}
	out, err := docker.Inspect(kind, r.id)
	if err != nil {
		InfoBar.Error(err.Error())
		return
	}
	openScratchTab(kind+"-inspect://"+r.name, out)
}

// openScratchTab opens read-only text content in a brand new tab.
func openScratchTab(name, content string) {
	b := buffer.NewBufferFromString(content, name, buffer.BTScratch)
	w, h := screen.Screen.Size()
	iOffset := config.GetInfoBarOffset()
	tp := NewTabFromBuffer(0, 0, w, h-iOffset, b)
	Tabs.AddTab(tp)
	Tabs.SetActive(len(Tabs.List) - 1)
	FocusedRegion = RegionEditor
}
