package actions

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/aclemen1/trigger"

	"github.com/aclemen1/guard-cli/internal/config"
	"github.com/aclemen1/guard-cli/internal/hooks"
	"github.com/aclemen1/guard-cli/internal/spec"
	"github.com/aclemen1/guard-cli/internal/store"
)

// Item is a rule with its sphere, as every action returns it.
type Item struct {
	store.Guard
	Sphere string `json:"sphere"`
	// Journal is what history.ls printed for the rule: guard show only.
	Journal string `json:"journal,omitempty"`
}

func ItemOf(s *store.Store, g *store.Guard) Item { return Item{Guard: *g, Sphere: s.Sphere} }

func idParam() spec.Param {
	return spec.Param{Name: "id", Kind: spec.String, Positional: true, Required: true, Help: "Rule id, e.g. PG-0007 (P: perso), UG-0007 (U: pro), or 7 with --sphere."}
}

func noteParam() spec.Param {
	return spec.Param{Name: "note", Kind: spec.String, Help: "Why, kept in the history."}
}

var linkRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*:\S.*$`)

// links reads repeatable, comma-separated <tool>:<id> values; none clears.
func links(ctx *spec.Context, name string) ([]string, bool, error) {
	vals := ctx.List(name)
	if len(vals) == 0 {
		return nil, false, nil
	}
	var out []string
	for _, v := range vals {
		for _, r := range strings.Split(v, ",") {
			r = strings.TrimSpace(r)
			if r == "" || r == "none" {
				continue
			}
			if !linkRe.MatchString(r) {
				return nil, true, spec.UserError("--%s %q: expected <tool>:<id>, e.g. case:flat-repairs", name, r)
			}
			if !contains(out, r) {
				out = append(out, r)
			}
		}
	}
	return out, true, nil
}

// checkTrigger accepts an event or a situation; none or empty clears.
func checkTrigger(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "none" {
		return "", nil
	}
	k, err := trigger.Parse(s)
	if err != nil {
		return "", spec.UserError("--trigger: %v", err)
	}
	if k.Kind() != trigger.On && k.Kind() != trigger.With {
		return "", spec.UserError("--trigger %q: a rule is recalled by an event (on:<tool>:<event>[:<id>]) or a situation (with:<…>), not by %s:", s, k.Kind())
	}
	return k.String(), nil
}

func checkSource(s string) error {
	if s != "" && !linkRe.MatchString(s) {
		return spec.UserError("--source %q: expected <tool>:<id>, e.g. mail:<message-id>", s)
	}
	return nil
}

func body(ctx *spec.Context) (string, bool, error) {
	b, set := ctx.Args["body"].(string)
	if !set {
		return "", false, nil
	}
	if b == "-" && ctx.Stdin != nil {
		raw, err := io.ReadAll(ctx.Stdin)
		if err != nil {
			return "", true, err
		}
		return strings.TrimSpace(string(raw)), true, nil
	}
	return b, true, nil
}

func withNote(what, note string) string {
	if n := strings.TrimSpace(note); n != "" {
		return what + ": " + n
	}
	return what
}

// errUnchanged tells change that the rule is already as asked: nothing is saved, committed or fired.
var errUnchanged = errors.New("unchanged")

// change runs f on a rule under its store's lock, saves it and fires the event.
func change(ctx *spec.Context, verb, event string, f func(s *store.Store, g *store.Guard) error) (any, error) {
	cfg, err := config.Load(ctx.Config)
	if err != nil {
		return nil, err
	}
	sphere, err := SphereOfGuard(ctx, cfg, ctx.Str("id"))
	if err != nil {
		return nil, err
	}
	s, err := OpenStore(ctx, cfg, sphere)
	if err != nil {
		return nil, err
	}
	var out *store.Guard
	changed := false
	err = s.WriteAs(func() (string, error) {
		g, err := s.Get(ctx.Str("id"))
		if err != nil {
			return "", err
		}
		out = g
		if err := f(s, g); err == errUnchanged {
			return "", nil
		} else if err != nil {
			return "", err
		}
		if err := s.Save(g); err != nil {
			return "", err
		}
		changed = true
		return fmt.Sprintf("%s %s: %s", verb, g.ID, g.Title), nil
	})
	if err != nil {
		return nil, err
	}
	if changed {
		fire(ctx, cfg, s, event, out, "")
	}
	return ItemOf(s, out), nil
}

func fire(ctx *spec.Context, cfg *config.Config, s *store.Store, event string, g *store.Guard, key string) {
	if len(cfg.Hooks) == 0 || event == "" {
		return
	}
	hooks.Fire(cfg, hooks.Event{Event: event, Sphere: s.Sphere, By: s.By, Key: key, Guard: g}, ctx.Warn)
}

func registerGuards() {
	spec.Register(&spec.Action{
		Category: "guard", Name: "add", Top: true,
		Summary: "Add a rule of conduct tied to an object: what must never, or only under a condition, be done.",
		Discussion: "A rule calls for no action; it forbids or conditions one. --ref names what it belongs to (repeatable). " +
			"--trigger is the key that recalls it: an event, on:<tool>:<event>[:<id>], or a situation, with:<…> (with:contact:<alias>). " +
			"--when says the situation in words. --source names where the rule comes from: one rule per source and sphere, " +
			"so a second add of the same source is refused, unless --upsert updates it. A rule for every object is not a rule of guard.",
		Params: []spec.Param{
			{Name: "title", Kind: spec.String, Positional: true, Required: true, Help: "The rule: « Never sign the debtor's commitment »."},
			writeSphereParam(),
			{Name: "ref", Kind: spec.StringList, Help: "What the rule belongs to, as <tool>:<id>; repeatable or comma-separated."},
			{Name: "trigger", Kind: spec.String, Help: "Key that recalls it: on:<tool>:<event>[:<id>] or with:<…>, e.g. with:contact:JMR."},
			{Name: "when", Kind: spec.String, Help: "The situation in words: « when the agency sends a paper to sign »."},
			{Name: "source", Kind: spec.String, Help: "Where it comes from, as <tool>:<id>; one rule per source."},
			{Name: "body", Kind: spec.String, Help: "Context of the rule; - reads stdin."},
			{Name: "upsert", Kind: spec.Bool, Help: "If --source already has a rule, update it with the fields given instead of refusing."},
		},
		Effects: []string{"Writes the rule's file in the sphere's store and commits it.", "Runs the hooks of added (edited with --upsert)."},
		Examples: []string{
			`guard add "Never sign the debtor's commitment" --ref case:deposit --trigger with:contact:AGENCY --when "when the agency sends a paper to sign" --sphere home`,
			`guard add "Nothing agreed without the insurer's consent" --ref case:deposit --when "before any agreement" --sphere home`,
			`guard add "Do not answer before the lawyer read it" --ref case:deposit --trigger on:mail:reply:thread/abc --sphere home`,
		},
		Run:  runAdd,
		Text: textItem,
	})
	spec.Register(&spec.Action{
		Category: "guard", Name: "show", Top: true,
		Summary:    "Show a rule: fields, context, history.",
		Discussion: "The id's prefix names its sphere (PG-0007 perso, UG-0007 pro); --sphere is needed only for a bare number.",
		Params:     []spec.Param{idParam(), idSphereParam()},
		Examples:   []string{"guard show PG-0007", "guard show 7 --sphere perso"},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, err := config.Load(ctx.Config)
			if err != nil {
				return nil, err
			}
			sphere, err := SphereOfGuard(ctx, cfg, ctx.Str("id"))
			if err != nil {
				return nil, err
			}
			s, err := OpenStore(ctx, cfg, sphere)
			if err != nil {
				return nil, err
			}
			g, err := s.Get(ctx.Str("id"))
			if err != nil {
				return nil, err
			}
			it := ItemOf(s, g)
			it.Journal = Journal(cfg, it)
			return it, nil
		},
		Text: textDetail,
	})
	spec.Register(&spec.Action{
		Category: "guard", Name: "edit", Top: true,
		Summary:    "Change a rule: title, refs, trigger, situation, source, context.",
		Discussion: "Only the options given change. --ref replaces every ref (--ref none clears); --add-ref and --remove-ref change one. --trigger none and an empty --when clear.",
		Params: []spec.Param{
			idParam(),
			{Name: "title", Kind: spec.String, Help: "New rule."},
			{Name: "ref", Kind: spec.StringList, Help: "New refs, replacing the others; none clears."},
			{Name: "add-ref", Kind: spec.StringList, Help: "Ref to add."},
			{Name: "remove-ref", Kind: spec.StringList, Help: "Ref to remove."},
			{Name: "trigger", Kind: spec.String, Help: "New trigger key, or none."},
			{Name: "when", Kind: spec.String, Help: "New situation in words; empty clears."},
			{Name: "source", Kind: spec.String, Help: "New source; empty clears."},
			{Name: "body", Kind: spec.String, Help: "New context; - reads stdin."},
			noteParam(),
			idSphereParam(),
		},
		Effects:  []string{"Rewrites the rule's file and commits it; nothing when nothing changes.", "Runs the hooks of edited."},
		Examples: []string{`guard edit PG-0001 --trigger with:contact:AGENCY`, `guard edit PG-0001 --add-ref contact:AGENCY`, `guard edit PG-0001 --when "" --trigger none`},
		Run: func(ctx *spec.Context) (any, error) {
			return change(ctx, "edit", "edited", func(s *store.Store, g *store.Guard) error {
				return apply(ctx, s, g, true)
			})
		},
		Text: textItem,
	})
	spec.Register(&spec.Action{
		Category: "guard", Name: "lift", Top: true,
		Summary:    "Lift a rule: it no longer holds, and stays in the history.",
		Discussion: "A lifted rule leaves guard ls and guard fire; restore brings it back. Lifting a lifted rule does nothing and succeeds.",
		Params:     []spec.Param{idParam(), noteParam(), idSphereParam()},
		Effects:    []string{"Sets the state to lifted and commits; nothing when already lifted.", "Runs the hooks of lifted."},
		Examples:   []string{`guard lift PG-0001 --note "settled by the agreement of 12.11.2026"`},
		Run: func(ctx *spec.Context) (any, error) {
			return change(ctx, "lift", "lifted", func(s *store.Store, g *store.Guard) error {
				if g.State == store.Lifted {
					return errUnchanged
				}
				s.SetState(g, store.Lifted)
				s.Note(g, withNote("lifted", ctx.Str("note")))
				return nil
			})
		},
		Text: textItem,
	})
	spec.Register(&spec.Action{
		Category: "guard", Name: "restore", Top: true,
		Summary:  "Restore a lifted rule: it holds again.",
		Params:   []spec.Param{idParam(), noteParam(), idSphereParam()},
		Effects:  []string{"Sets the state to active and commits; nothing when already active.", "Runs the hooks of restored."},
		Examples: []string{"guard restore PG-0001"},
		Run: func(ctx *spec.Context) (any, error) {
			return change(ctx, "restore", "restored", func(s *store.Store, g *store.Guard) error {
				if g.State == store.Active {
					return errUnchanged
				}
				s.SetState(g, store.Active)
				s.Note(g, withNote("restored", ctx.Str("note")))
				return nil
			})
		},
		Text: textItem,
	})
	spec.Register(&spec.Action{
		Category: "guard", Name: "rm", Top: true, Destructive: true,
		Summary:    "Delete a rule for good. To end a rule that held, lift it instead.",
		Discussion: "For a rule added by mistake. The history of the store keeps the file.",
		Params:     []spec.Param{idParam(), idSphereParam()},
		Effects:    []string{"Deletes the rule's file and commits.", "Runs the hooks of removed."},
		Examples:   []string{"guard rm PG-0009"},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, err := config.Load(ctx.Config)
			if err != nil {
				return nil, err
			}
			sphere, err := SphereOfGuard(ctx, cfg, ctx.Str("id"))
			if err != nil {
				return nil, err
			}
			s, err := OpenStore(ctx, cfg, sphere)
			if err != nil {
				return nil, err
			}
			var gone *store.Guard
			err = s.WriteAs(func() (string, error) {
				g, err := s.Get(ctx.Str("id"))
				if err != nil {
					return "", err
				}
				gone = g
				if err := s.Remove(g.ID); err != nil {
					return "", err
				}
				return fmt.Sprintf("remove %s: %s", g.ID, g.Title), nil
			})
			if err != nil {
				return nil, err
			}
			fire(ctx, cfg, s, "removed", gone, "")
			return ItemOf(s, gone), nil
		},
		Text: textItem,
	})
	spec.Register(&spec.Action{
		Category: "guard", Name: "ls", Top: true,
		Summary: "List the rules: those of an object (--ref) are what an agent reads before acting on it.",
		Discussion: "Active rules of every sphere by default. --ref keeps the rules that cite one of the refs given. " +
			"--trigger keeps the rules whose trigger starts with the segments given: with: gives every rule of a situation, " +
			"with:contact:JMR those of one contact. --search looks in the rule, the situation and the context.",
		Params: []spec.Param{
			readSphereParam(),
			{Name: "ref", Kind: spec.StringList, Help: "Keep the rules citing this ref, e.g. case:flat-repairs; repeatable."},
			{Name: "trigger", Kind: spec.String, Help: "Keep the rules whose trigger starts with these segments, e.g. with: or on:mail."},
			{Name: "state", Kind: spec.String, Default: store.Active, Enum: []string{store.Active, store.Lifted, "all"}, Help: "Which rules."},
			{Name: "search", Kind: spec.String, Help: "Words that must all appear in the rule, situation or context."},
			{Name: "source", Kind: spec.String, Help: "Keep the rules whose source starts with this."},
		},
		Examples: []string{"guard ls --ref case:flat-repairs", "guard ls --trigger with: --format text", "guard ls --state all --sphere perso"},
		Run:      runList,
		Text:     textList,
	})
	spec.Register(&spec.Action{
		Category: "guard", Name: "fire", Top: true,
		Summary: "Present the rules whose trigger matches a key that just happened, and run the hooks of fired.",
		Discussion: "A tool's hook calls it when an event comes: guard fire on:mail:reply:thread/abc. A trigger matches when its segments " +
			"start the key's: on:mail:reply matches on:mail:reply:thread/abc, on:mail:rep does not. Only active rules match. " +
			"Each match runs the hooks of fired, with the key; --dry-run only lists.",
		Params: []spec.Param{
			{Name: "key", Kind: spec.String, Positional: true, Required: true, Help: "The key that happened: on:<tool>:<event>[:<id>] or with:<…>."},
			readSphereParam(),
			{Name: "dry-run", Kind: spec.Bool, Help: "List the matches without running the hooks."},
		},
		Effects:  []string{"Runs the hooks of fired for each match; writes nothing."},
		Examples: []string{"guard fire on:mail:reply:thread/abc", "guard fire with:contact:JMR --dry-run --format text"},
		Run:      runFire,
		Text:     textList,
	})
	spec.Register(&spec.Action{
		Category: "guard", Name: "note", Top: true,
		Summary:    "Add a note about a rule, through the notes command of the configuration (notes.run).",
		Discussion: "guard keeps no notes itself: the command reads {\"sphere\",\"guard\",\"text\"} on stdin and keeps the note where notes live.",
		Params: []spec.Param{
			idParam(),
			{Name: "text", Kind: spec.String, Positional: true, Required: true, Help: "The note."},
			idSphereParam(),
		},
		Effects:  []string{"Runs notes.run; guard's store does not change."},
		Examples: []string{`guard note PG-0001 "The agency sent a new paper on 12.11; not signed."`},
		Run:      runNote,
	})
}

// apply sets the fields given on a rule; with edit, a rule left as it was is errUnchanged.
func apply(ctx *spec.Context, s *store.Store, g *store.Guard, edit bool) error {
	before, _ := json.Marshal(g)
	var what []string
	if t, ok := ctx.Args["title"].(string); ok && strings.TrimSpace(t) != "" && t != g.Title {
		g.Title = strings.TrimSpace(t)
		what = append(what, "title")
	}
	refs, set, err := links(ctx, "ref")
	if err != nil {
		return err
	}
	if set {
		g.Refs = refs
	}
	adds, _, err := links(ctx, "add-ref")
	if err != nil {
		return err
	}
	for _, r := range adds {
		if !contains(g.Refs, r) {
			g.Refs = append(g.Refs, r)
		}
	}
	rms, _, err := links(ctx, "remove-ref")
	if err != nil {
		return err
	}
	if len(rms) > 0 {
		var keep []string
		for _, r := range g.Refs {
			if !contains(rms, r) {
				keep = append(keep, r)
			}
		}
		g.Refs = keep
	}
	if t, ok := ctx.Args["trigger"].(string); ok {
		k, err := checkTrigger(t)
		if err != nil {
			return err
		}
		g.Trigger = k
	}
	if w, ok := ctx.Args["when"].(string); ok {
		g.When = strings.TrimSpace(w)
	}
	if src, ok := ctx.Args["source"].(string); ok {
		src = strings.TrimSpace(src)
		if err := checkSource(src); err != nil {
			return err
		}
		g.Source = src
	}
	b, set, err := body(ctx)
	if err != nil {
		return err
	}
	if set {
		g.Body = b
	}
	after, _ := json.Marshal(g)
	if edit && string(before) == string(after) {
		return errUnchanged
	}
	if edit {
		s.Note(g, withNote("edited", ctx.Str("note")))
	}
	return nil
}

func runAdd(ctx *spec.Context) (any, error) {
	cfg, err := config.Load(ctx.Config)
	if err != nil {
		return nil, err
	}
	sphere := ctx.Str("sphere")
	if err := checkSphere(ctx, cfg, sphere); err != nil {
		return nil, err
	}
	if strings.TrimSpace(ctx.Str("title")) == "" {
		return nil, spec.UserError("the rule is empty. Example: guard add \"Never sign the debtor's commitment\" --sphere home")
	}
	s, err := OpenStore(ctx, cfg, sphere)
	if err != nil {
		return nil, err
	}
	source := strings.TrimSpace(ctx.Str("source"))
	if err := checkSource(source); err != nil {
		return nil, err
	}
	var out *store.Guard
	event := ""
	err = s.WriteAs(func() (string, error) {
		if source != "" {
			old, err := s.BySource(source)
			if err != nil {
				return "", err
			}
			if old != nil {
				if !ctx.Bool("upsert") {
					return "", spec.Conflict("source %s already has rule %s (%s); --upsert updates it", source, old.ID, old.Title)
				}
				out = old
				if err := apply(ctx, s, old, true); err == errUnchanged {
					return "", nil
				} else if err != nil {
					return "", err
				}
				if err := s.Save(old); err != nil {
					return "", err
				}
				event = "edited"
				return fmt.Sprintf("edit %s: %s", old.ID, old.Title), nil
			}
		}
		now := s.Now().Format(time.RFC3339)
		g := &store.Guard{ID: s.NextID(), State: store.Active, By: s.By, Created: now}
		if err := apply(ctx, s, g, false); err != nil {
			return "", err
		}
		s.Note(g, "added")
		if err := s.Save(g); err != nil {
			return "", err
		}
		out, event = g, "added"
		return fmt.Sprintf("add %s: %s", g.ID, g.Title), nil
	})
	if err != nil {
		return nil, err
	}
	fire(ctx, cfg, s, event, out, "")
	return ItemOf(s, out), nil
}

// each runs f on the rules of the spheres a read covers.
func each(ctx *spec.Context, f func(cfg *config.Config, s *store.Store, g *store.Guard)) error {
	cfg, err := config.Load(ctx.Config)
	if err != nil {
		return err
	}
	spheres, err := ReadSpheres(ctx, cfg)
	if err != nil {
		return err
	}
	for _, sp := range spheres {
		s, err := OpenStore(ctx, cfg, sp)
		if err != nil {
			return err
		}
		gs, err := s.List()
		if err != nil {
			return err
		}
		for _, g := range gs {
			f(cfg, s, g)
		}
	}
	return nil
}

func runList(ctx *spec.Context) (any, error) {
	refs, _, err := links(ctx, "ref")
	if err != nil {
		return nil, err
	}
	state := ctx.Str("state")
	prefix := ctx.Str("trigger")
	words := strings.Fields(strings.ToLower(ctx.Str("search")))
	source := ctx.Str("source")
	out := []Item{}
	err = each(ctx, func(_ *config.Config, s *store.Store, g *store.Guard) {
		if state != "all" && g.State != state {
			return
		}
		if len(refs) > 0 && !citesAny(g, refs) {
			return
		}
		if prefix != "" && (g.Trigger == "" || !trigger.HasPrefix(g.Trigger, prefix)) {
			return
		}
		if source != "" && !strings.HasPrefix(g.Source, source) {
			return
		}
		hay := strings.ToLower(g.Title + " " + g.When + " " + g.Body)
		for _, w := range words {
			if !strings.Contains(hay, w) {
				return
			}
		}
		out = append(out, ItemOf(s, g))
	})
	return out, err
}

func citesAny(g *store.Guard, refs []string) bool {
	for _, r := range g.Refs {
		if contains(refs, r) {
			return true
		}
	}
	return false
}

func runFire(ctx *spec.Context) (any, error) {
	key, err := trigger.Parse(ctx.Str("key"))
	if err != nil {
		return nil, spec.UserError("%v", err)
	}
	type match struct {
		cfg *config.Config
		s   *store.Store
		g   *store.Guard
	}
	var matches []match
	out := []Item{}
	err = each(ctx, func(cfg *config.Config, s *store.Store, g *store.Guard) {
		if g.State != store.Active || g.Trigger == "" {
			return
		}
		t, err := trigger.Parse(g.Trigger)
		if err != nil || !t.Starts(key) {
			return
		}
		matches = append(matches, match{cfg, s, g})
		out = append(out, ItemOf(s, g))
	})
	if err != nil {
		return nil, err
	}
	if !ctx.Bool("dry-run") {
		for _, m := range matches {
			fire(ctx, m.cfg, m.s, "fired", m.g, key.String())
		}
	}
	return out, nil
}

func runNote(ctx *spec.Context) (any, error) {
	cfg, err := config.Load(ctx.Config)
	if err != nil {
		return nil, err
	}
	if len(cfg.Notes.Run) == 0 {
		return nil, spec.UserError("no notes command: set notes.run in %s, a command that reads {\"sphere\",\"guard\",\"text\"} on stdin", cfg.File())
	}
	text := strings.TrimSpace(ctx.Str("text"))
	if text == "" {
		return nil, spec.UserError("the note is empty. Example: guard note PG-0001 \"Not signed.\"")
	}
	sphere, err := SphereOfGuard(ctx, cfg, ctx.Str("id"))
	if err != nil {
		return nil, err
	}
	s, err := OpenStore(ctx, cfg, sphere)
	if err != nil {
		return nil, err
	}
	g, err := s.Get(ctx.Str("id"))
	if err != nil {
		return nil, err
	}
	in, _ := json.Marshal(map[string]any{"sphere": sphere, "guard": ItemOf(s, g), "text": text})
	printed, err := hooks.Run(cfg.Notes.Run, cfg.Notes.Timeout, in,
		[]string{"GUARD_ID=" + g.ID, "GUARD_SPHERE=" + sphere, "GUARD_BY=" + s.By})
	if err != nil {
		return nil, spec.Internal(fmt.Errorf("notes.run: %v", err))
	}
	return map[string]any{"id": g.ID, "sphere": sphere, "output": printed}, nil
}

// Open runs open.run of the configuration for a rule and returns what it printed.
func Open(cfgPath string, it Item) (string, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return "", err
	}
	if len(cfg.Open.Run) == 0 {
		return "", fmt.Errorf("no open command: set open.run in %s", cfg.File())
	}
	in, _ := json.Marshal(map[string]any{"sphere": it.Sphere, "guard": it})
	return hooks.Run(cfg.Open.Run, cfg.Open.Timeout, in, []string{"GUARD_ID=" + it.ID, "GUARD_SPHERE=" + it.Sphere})
}

func textItem(w io.Writer, v any) {
	it, ok := v.(Item)
	if !ok {
		return
	}
	fmt.Fprintln(w, Line(it))
}

func textList(w io.Writer, v any) {
	items, _ := v.([]Item)
	for _, it := range items {
		fmt.Fprintln(w, Line(it))
	}
}

// Line is a rule on one line: id, rule, situation, refs.
func Line(it Item) string {
	var b strings.Builder
	b.WriteString(it.ID + "  " + it.Title)
	if it.State == store.Lifted {
		b.WriteString("  (lifted)")
	}
	if s := Situation(it.Guard); s != "" {
		b.WriteString("  — " + s)
	}
	if len(it.Refs) > 0 {
		b.WriteString("  [" + strings.Join(it.Refs, ", ") + "]")
	}
	return b.String()
}

// Situation joins the words and the key of a trigger.
func Situation(g store.Guard) string {
	switch {
	case g.When != "" && g.Trigger != "":
		return g.When + " (" + g.Trigger + ")"
	case g.When != "":
		return g.When
	}
	return g.Trigger
}

func textDetail(w io.Writer, v any) {
	it, ok := v.(Item)
	if !ok {
		return
	}
	fmt.Fprintf(w, "%s  %s\n\n", it.ID, it.Title)
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(w, "%-9s %s\n", k, v)
		}
	}
	row("state", it.State)
	row("sphere", it.Sphere)
	row("when", it.When)
	row("trigger", it.Trigger)
	row("refs", strings.Join(it.Refs, ", "))
	row("source", it.Source)
	row("by", it.By)
	row("created", it.Created)
	row("lifted", it.Lifted)
	if it.Body != "" {
		fmt.Fprintf(w, "\n%s\n", it.Body)
	}
	if len(it.Log) > 0 {
		fmt.Fprintln(w, "\nhistory:")
		for _, l := range it.Log {
			fmt.Fprintf(w, "  %s  %s  %s\n", l.At, l.By, l.What)
		}
	}
	if it.Journal != "" {
		fmt.Fprintf(w, "\njournal:\n%s\n", it.Journal)
	}
}

// Journal runs history.ls of the configuration for a rule; empty when there is
// none, or when the command fails.
func Journal(cfg *config.Config, it Item) string {
	if len(cfg.History.Ls) == 0 {
		return ""
	}
	timeout := cfg.History.Timeout
	if timeout == "" {
		timeout = "5s"
	}
	argv := make([]string, len(cfg.History.Ls))
	for i, a := range cfg.History.Ls {
		argv[i] = strings.NewReplacer("{id}", it.ID, "{sphere}", it.Sphere).Replace(a)
	}
	out, err := hooks.Run(argv, timeout, nil, nil)
	if err != nil {
		return ""
	}
	return out
}
