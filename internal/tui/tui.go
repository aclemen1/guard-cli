// Package tui is guard's terminal interface: the rules by state and sphere,
// the detail of the selected one, modal inputs, live updates from the files.
package tui

import (
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/aclemen1/tuikit"
	"github.com/fsnotify/fsnotify"

	"github.com/aclemen1/guard-cli/internal/actions"
	"github.com/aclemen1/guard-cli/internal/config"
	"github.com/aclemen1/guard-cli/internal/spec"
	"github.com/aclemen1/guard-cli/internal/store"
)

func init() {
	spec.Register(&spec.Action{
		Category: "setup", Name: "tui", Top: true,
		Summary:  "Open the terminal interface: the rules by state and sphere, their detail, modal inputs.",
		Params:   []spec.Param{{Name: "sphere", Kind: spec.String, Help: "Show only this sphere. Defaults to $GUARD_SPHERE, else every sphere."}},
		Effects:  []string{"Runs until q; every change goes through the same actions as the CLI."},
		Examples: []string{"guard tui", "guard tui --sphere pro"},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, err := config.Load(ctx.Config)
			if err != nil {
				return nil, err
			}
			spheres, err := actions.ReadSpheres(ctx, cfg)
			if err != nil {
				return nil, err
			}
			m := newModel(ctx.Config, cfg, spheres)
			if w := m.watch(); w != nil {
				defer w.Close()
			}
			m.exe = executable()
			m.exeStamp = stamp(m.exe)
			m.restore()
			_, err = tea.NewProgram(m).Run()
			if err == nil && m.restart {
				return spec.Streamed{}, m.reexec()
			}
			return spec.Streamed{}, err
		},
	})
}

var views = []string{"Actives", "Levées", "Toutes"}

var sorts = []string{"id", "règle", "réf."}

type model struct {
	cfgPath string
	cfg     *config.Config
	spheres []string

	all    []actions.Item
	items  []actions.Item
	err    string
	view   int
	filter string
	only   string // one sphere among those shown, or all
	sortBy int
	rev    bool

	sel, top, scroll int
	detailOn, cardOn bool
	helpOn           bool
	pendingG         bool

	filtering bool
	input     textinput.Model
	modal     *tuikit.Modal
	target    actions.Item // the rule a modal is about
	editing   bool

	w, h      int
	status    string
	statusErr bool
	statusAt  time.Time

	events   chan struct{}
	signals  chan os.Signal
	exe      string
	exeStamp string
	newStamp string
	restart  bool
	// restoreSel is the rule selected before a restart, chosen again at the first read.
	restoreSel string
}

type loadedMsg struct {
	items []actions.Item
	err   error
}

type doneMsg struct {
	status, sel string
	err         error
}

type filesMsg struct{}
type signalMsg struct{}
type tickMsg struct{}

func newModel(cfgPath string, cfg *config.Config, spheres []string) *model {
	in := textinput.New()
	in.Prompt = ""
	m := &model{cfgPath: cfgPath, cfg: cfg, spheres: spheres, input: in, w: 100, h: 30, detailOn: true}
	m.signals = make(chan os.Signal, 1)
	signal.Notify(m.signals, syscall.SIGUSR1)
	return m
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.load(), m.waitFiles(), m.waitSignal(), tick())
}

func tick() tea.Cmd { return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }

func (m *model) waitSignal() tea.Cmd {
	ch := m.signals
	return func() tea.Msg { <-ch; return signalMsg{} }
}

func (m *model) multi() bool { return len(m.spheres) > 1 }

func (m *model) ctx(args map[string]any) *spec.Context {
	return &spec.Context{Args: args, Config: m.cfgPath, Format: "json"}
}

// load reads every rule of the spheres shown, whatever its state.
func (m *model) load() tea.Cmd {
	spheres := m.spheres
	return func() tea.Msg {
		a := spec.Find("guard", "ls")
		var all []actions.Item
		for _, sp := range spheres {
			res, err := a.Run(m.ctx(map[string]any{"state": "all", "sphere": sp}))
			if err != nil {
				return loadedMsg{err: err}
			}
			all = append(all, res.([]actions.Item)...)
		}
		return loadedMsg{items: all}
	}
}

// act runs an action as the CLI would, then reloads.
func (m *model) act(name string, args map[string]any, status string) tea.Cmd {
	return func() tea.Msg {
		a := spec.Find("guard", name)
		ctx := m.ctx(args)
		res, err := a.Run(ctx)
		if err != nil {
			return doneMsg{err: err}
		}
		sel := ""
		if it, ok := res.(actions.Item); ok {
			sel = it.ID
			if status == "" {
				status = it.ID
			}
		}
		if len(ctx.Warnings) > 0 {
			status += " (" + strings.Join(ctx.Warnings, " ; ") + ")"
		}
		return doneMsg{status: status, sel: sel}
	}
}

func (m *model) say(s string, isErr bool) {
	m.status, m.statusErr, m.statusAt = s, isErr, time.Now()
}

// apply filters and sorts the rules of the current view, keeping the selection.
func (m *model) apply() {
	keep := ""
	if it, ok := m.current(); ok {
		keep = it.ID
	}
	words := strings.Fields(strings.ToLower(m.filter))
	var out []actions.Item
	for _, it := range m.all {
		switch {
		case m.view == 0 && it.State != store.Active,
			m.view == 1 && it.State != store.Lifted,
			m.only != "" && it.Sphere != m.only:
			continue
		}
		hay := strings.ToLower(strings.Join([]string{it.ID, it.Title, it.When, it.Trigger, strings.Join(it.Refs, " "), it.Source, it.Body}, " "))
		ok := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				ok = false
			}
		}
		if ok {
			out = append(out, it)
		}
	}
	key := func(it actions.Item) string {
		switch m.sortBy {
		case 1:
			return strings.ToLower(it.Title)
		case 2:
			return strings.Join(it.Refs, ",")
		}
		return it.ID
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.State != b.State {
			return a.State == store.Active
		}
		if a.Sphere != b.Sphere {
			return a.Sphere < b.Sphere
		}
		if m.rev {
			return key(a) > key(b)
		}
		return key(a) < key(b)
	})
	m.items = out
	m.sel = 0
	for i, it := range out {
		if it.ID == keep {
			m.sel = i
		}
	}
	m.clamp()
}

func (m *model) clamp() {
	if m.sel >= len(m.items) {
		m.sel = len(m.items) - 1
	}
	if m.sel < 0 {
		m.sel = 0
	}
}

func (m *model) current() (actions.Item, bool) {
	if m.sel < 0 || m.sel >= len(m.items) {
		return actions.Item{}, false
	}
	return m.items[m.sel], true
}

func (m *model) selectIndex(i int) {
	old := m.sel
	m.sel = i
	m.clamp()
	if m.sel != old {
		m.scroll = 0
	}
}

func group(it actions.Item) string { return it.State + "/" + it.Sphere }

// groupStart is the first line of the next (dir 1) or previous (dir -1) section.
func (m *model) groupStart(dir int) int {
	if len(m.items) == 0 {
		return 0
	}
	i := m.sel
	cur := group(m.items[i])
	if dir > 0 {
		for i < len(m.items)-1 && group(m.items[i]) == cur {
			i++
		}
		return i
	}
	for i > 0 && group(m.items[i-1]) == cur {
		i--
	}
	if i == m.sel && i > 0 {
		i--
		prev := group(m.items[i])
		for i > 0 && group(m.items[i-1]) == prev {
			i--
		}
	}
	return i
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.modal.Open() {
		switch msg.(type) {
		case loadedMsg, doneMsg, filesMsg, signalMsg, tickMsg, tuikit.DoneMsg, tuikit.CancelMsg, tea.BackgroundColorMsg, tea.WindowSizeMsg:
		default:
			return m, m.modal.Update(msg)
		}
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.input.SetWidth(max(10, m.w-20))
		if m.modal.Open() {
			return m, m.modal.Update(msg)
		}
	case tea.BackgroundColorMsg:
		darkBackground = msg.IsDark()
		tuikit.SetDarkBackground(msg.IsDark())
	case tuikit.DoneMsg:
		m.modal = nil
		return m, m.done(msg)
	case tuikit.CancelMsg:
		m.modal = nil
	case loadedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.err = ""
		m.all = msg.items
		m.apply()
		if m.restoreSel != "" {
			for i, it := range m.items {
				if it.ID == m.restoreSel {
					m.selectIndex(i)
				}
			}
			m.restoreSel = ""
		}
	case doneMsg:
		if msg.err != nil {
			m.say(msg.err.Error(), true)
			return m, nil
		}
		m.say(msg.status, false)
		cmd := m.load()
		if msg.sel != "" {
			sel := msg.sel
			return m, func() tea.Msg { return selectAfter{cmd().(loadedMsg), sel} }
		}
		return m, cmd
	case selectAfter:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.all = msg.items
		m.apply()
		for i, it := range m.items {
			if it.ID == msg.sel {
				m.selectIndex(i)
			}
		}
	case filesMsg:
		return m, tea.Batch(m.load(), m.waitFiles())
	case signalMsg:
		return m, tea.Batch(m.load(), m.waitSignal())
	case tickMsg:
		if m.status != "" && !m.statusErr && time.Since(m.statusAt) > 5*time.Second {
			m.status = ""
		}
		if m.checkBin() {
			m.restart = true
			return m, tea.Quit
		}
		return m, tick()
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.filtering {
			return m, m.keyFilter(msg)
		}
		if m.statusErr {
			m.status, m.statusErr = "", false
		}
		if msg.String() == "q" {
			return m, tea.Quit
		}
		return m, m.keyList(msg)
	}
	return m, nil
}

type selectAfter struct {
	loadedMsg
	sel string
}

func (m *model) keyFilter(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.filtering = false
		m.filter = ""
		m.input.Blur()
		m.apply()
		return nil
	case "enter":
		m.filtering = false
		m.input.Blur()
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	m.filter = m.input.Value()
	m.apply()
	return cmd
}

func (m *model) setView(i int) {
	m.view = (i + len(views)) % len(views)
	m.top, m.scroll = 0, 0
	m.apply()
}

// keyList follows the shared key convention of the TUIs.
func (m *model) keyList(k tea.KeyPressMsg) tea.Cmd {
	page := max(1, m.h/2)
	s := k.String()
	if s != "g" {
		m.pendingG = false
	}
	switch s {
	case "up", "k":
		m.selectIndex(m.sel - 1)
	case "down", "j":
		m.selectIndex(m.sel + 1)
	case "pgup", "ctrl+u", "ctrl+b":
		m.selectIndex(m.sel - page)
	case "pgdown", "ctrl+d", "ctrl+f":
		m.selectIndex(m.sel + page)
	case "g":
		if m.pendingG {
			m.pendingG = false
			m.selectIndex(0)
		} else {
			m.pendingG = true
		}
	case "home":
		m.selectIndex(0)
	case "end", "G":
		m.selectIndex(len(m.items) - 1)
	case "J":
		m.scroll += 3
	case "K":
		m.scroll = max(0, m.scroll-3)
	case "[":
		m.selectIndex(m.groupStart(-1))
	case "]":
		m.selectIndex(m.groupStart(1))
	case "tab":
		m.detailOn = !m.detailOn
	case "enter", "l", "right":
		if _, ok := m.current(); ok {
			m.cardOn = true
		}
	case "esc", "h", "left":
		switch {
		case m.helpOn:
			m.helpOn = false
		case m.cardOn:
			m.cardOn = false
		case m.filter != "":
			m.filter = ""
			m.apply()
		case m.only != "":
			m.only = ""
			m.apply()
		case m.view != 0:
			m.setView(0)
		}
	case "?":
		m.helpOn = !m.helpOn
	case "/":
		m.filtering = true
		m.input.Placeholder = "règle, situation, réf…"
		m.input.SetValue(m.filter)
		m.input.CursorEnd()
		return m.input.Focus()
	case "1", "2", "3":
		n, _ := strconv.Atoi(s)
		m.setView(n - 1)
	case "s":
		m.cycleSphere()
	case "t":
		m.sortBy = (m.sortBy + 1) % len(sorts)
		m.apply()
		m.say("tri par "+sorts[m.sortBy], false)
	case "T":
		m.rev = !m.rev
		m.apply()
		m.say(map[bool]string{true: "tri inversé", false: "tri normal"}[m.rev], false)
	case "r":
		m.say("relecture", false)
		return m.load()
	case "c", "n":
		m.openForm(nil)
	case "W", "z", "*", "R":
		m.say("sans objet dans guard : une consigne n'attend personne, ne se reporte pas et n'a pas d'étoile", true)
	default:
		return m.keyRule(s)
	}
	return nil
}

// keyRule handles the actions on the selected rule.
func (m *model) keyRule(key string) tea.Cmd {
	it, ok := m.current()
	if !ok {
		return nil
	}
	ids := map[string]any{"id": it.ID, "sphere": it.Sphere}
	switch key {
	case "e", "x":
		if it.State == store.Lifted {
			m.say(it.ID+" est déjà levée (espace pour la rétablir)", false)
			return nil
		}
		return m.act("lift", ids, it.ID+" levée")
	case "space":
		if it.State == store.Lifted {
			return m.act("restore", ids, it.ID+" rétablie")
		}
		return m.act("lift", ids, it.ID+" levée")
	case "E":
		m.openForm(&it)
	case "N":
		if len(m.cfg.Notes.Run) == 0 {
			m.say("aucune commande de notes : notes.run dans "+m.cfg.File(), true)
			return nil
		}
		m.target = it
		m.modal = tuikit.NewModal("Note sur "+it.ID, tuikit.NewEditor("note", "Note", "")).SetSize(m.w, m.h)
	case "#":
		m.target = it
		m.modal = tuikit.NewModal("Supprimer", tuikit.NewConfirmTyped("rm",
			"Supprimer définitivement « "+it.Title+" » ? Pour une consigne qui ne vaut plus, levez-la (e). Recopiez son id.", it.ID)).SetSize(m.w, m.h)
	case "o":
		cfgPath := m.cfgPath
		return func() tea.Msg {
			out, err := actions.Open(cfgPath, it)
			if err != nil {
				return doneMsg{err: err}
			}
			if out == "" {
				out = it.ID + " : référence ouverte"
			}
			return doneMsg{status: out}
		}
	}
	return nil
}

func (m *model) cycleSphere() {
	if !m.multi() {
		m.say("une seule sphère est montrée", false)
		return
	}
	order := append([]string{""}, m.spheres...)
	for i, s := range order {
		if s == m.only {
			m.only = order[(i+1)%len(order)]
			break
		}
	}
	m.apply()
	if m.only == "" {
		m.say("toutes les sphères", false)
	} else {
		m.say("sphère "+m.only+" seulement", false)
	}
}

// openForm adds a rule, or edits one.
func (m *model) openForm(edit *actions.Item) {
	var fields []*tuikit.Field
	if edit == nil && m.multi() {
		fields = append(fields, tuikit.Choice("sphere", "Sphère", m.spheres...).Required().
			Help("selon la nature de la consigne ; jamais par défaut"))
	}
	title := tuikit.TextArea("title", "Règle").Required().Help("la consigne elle-même : « Ne jamais signer… », « Rien sans l'accord de… »")
	when := tuikit.Text("when", "Quand").Help("la situation en mots : « quand l'agence envoie un document à signer »")
	trig := tuikit.Ref("trigger", "Déclencheur", m.triggers).Help("on:<outil>:<événement>[:<id>] ou with:<…> (with:contact:<alias>) ; facultatif")
	refs := tuikit.Refs("refs", "Réfs", m.refs).Help("ce à quoi la consigne se rattache, <outil>:<id> ; entrée ou virgule pour en ajouter une")
	body := tuikit.TextArea("body", "Contexte")
	label := "Nouvelle consigne"
	m.editing = edit != nil
	if edit != nil {
		m.target = *edit
		label = "Modifier " + edit.ID
		title.Default(edit.Title)
		when.Default(edit.When)
		trig.Default(edit.Trigger)
		refs.Default(edit.Refs)
		body.Default(edit.Body)
	}
	fields = append(fields, title, when, trig, refs, body)
	m.modal = tuikit.NewModal(label, tuikit.NewForm("rule", fields...)).SetSize(m.w, m.h)
}

// triggers completes a trigger from those already in use.
func (m *model) triggers(q string) []tuikit.Item {
	q = strings.ToLower(strings.TrimSpace(q))
	seen := map[string]bool{}
	var out []tuikit.Item
	for _, v := range []string{"with:contact:", "on:"} {
		if strings.HasPrefix(v, q) || q == "" {
			out = append(out, tuikit.Item{Value: v, Label: v})
			seen[v] = true
		}
	}
	for _, it := range m.all {
		v := it.Trigger
		if v == "" || seen[v] || (q != "" && !strings.Contains(strings.ToLower(v), q)) {
			continue
		}
		seen[v] = true
		out = append(out, tuikit.Item{Value: v, Label: v + "  " + it.ID})
	}
	return out
}

// refs completes a ref from those the rules already cite.
func (m *model) refs(q string) []tuikit.Item {
	q = strings.ToLower(strings.TrimSpace(q))
	seen := map[string]bool{}
	var out []tuikit.Item
	for _, it := range m.all {
		for _, r := range it.Refs {
			if seen[r] || (q != "" && !strings.Contains(strings.ToLower(r), q)) {
				continue
			}
			seen[r] = true
			out = append(out, tuikit.Item{Value: r, Label: r})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

// done reads the answer of a modal.
func (m *model) done(msg tuikit.DoneMsg) tea.Cmd {
	v := msg.Values
	it := m.target
	switch msg.ID {
	case "rule":
		refs := v.Strings("refs")
		args := map[string]any{"title": strings.Join(strings.Fields(v.String("title")), " "), "when": v.String("when"),
			"trigger": v.String("trigger"), "body": v.String("body")}
		if !m.editing {
			args["sphere"] = v.String("sphere")
			if !m.multi() {
				args["sphere"] = m.spheres[0]
			}
			if len(refs) > 0 {
				args["ref"] = refs
			}
			if args["trigger"] == "" {
				delete(args, "trigger")
			}
			return m.act("add", args, "consigne ajoutée")
		}
		args["id"], args["sphere"] = it.ID, it.Sphere
		if args["trigger"] == "" {
			args["trigger"] = "none"
		}
		args["ref"] = refs
		if len(refs) == 0 {
			args["ref"] = []string{"none"}
		}
		return m.act("edit", args, it.ID+" modifiée")
	case "note":
		text := strings.TrimSpace(v.String("note"))
		if text == "" {
			m.say("note vide : rien n'est ajouté", false)
			return nil
		}
		return m.act("note", map[string]any{"id": it.ID, "sphere": it.Sphere, "text": text}, "note ajoutée à "+it.ID)
	case "rm":
		return m.act("rm", map[string]any{"id": it.ID, "sphere": it.Sphere}, it.ID+" supprimée")
	}
	return nil
}

// watch follows the stores; nil if it cannot.
func (m *model) watch() *fsnotify.Watcher {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil
	}
	for _, sp := range m.spheres {
		if s, ok := m.cfg.Spheres[sp]; ok {
			_ = w.Add(s.Root)
		}
	}
	m.events = make(chan struct{}, 1)
	go func() {
		var timer *time.Timer
		for {
			select {
			case e, ok := <-w.Events:
				if !ok {
					return
				}
				if !strings.HasSuffix(e.Name, ".md") {
					continue
				}
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(300*time.Millisecond, func() {
					select {
					case m.events <- struct{}{}:
					default:
					}
				})
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return w
}

func (m *model) waitFiles() tea.Cmd {
	if m.events == nil {
		return nil
	}
	ch := m.events
	return func() tea.Msg { <-ch; return filesMsg{} }
}
