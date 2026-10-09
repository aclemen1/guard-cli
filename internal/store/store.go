// Package store keeps the rules of a sphere: one Markdown file per rule,
// front matter for the fields, body for the context.
package store

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aclemen1/guard-cli/internal/config"
	"github.com/aclemen1/guard-cli/internal/spec"
)

const (
	Active = "active"
	Lifted = "lifted"
)

var States = []string{Active, Lifted}

type LogEntry struct {
	At   string `yaml:"at" json:"at"`
	By   string `yaml:"by" json:"by"`
	What string `yaml:"what" json:"what"`
}

// Guard is a rule of conduct tied to objects, recalled in a situation.
type Guard struct {
	ID string `yaml:"id" json:"id"`
	// Title is the rule itself: « Never sign the debtor's commitment ».
	Title string `yaml:"title" json:"title"`
	State string `yaml:"state" json:"state"`
	// Refs are what the rule belongs to, as <tool>:<id>.
	Refs []string `yaml:"refs,omitempty" json:"refs,omitempty"`
	// Trigger is the key that recalls the rule: on:<tool>:<event>[:<id>] or with:<…>.
	Trigger string `yaml:"trigger,omitempty" json:"trigger,omitempty"`
	// When says the situation in words: « when the agency sends a paper to sign ».
	When string `yaml:"when,omitempty" json:"when,omitempty"`
	// Source is where the rule comes from, as <tool>:<id>; one rule per source and sphere.
	Source  string     `yaml:"source,omitempty" json:"source,omitempty"`
	By      string     `yaml:"by,omitempty" json:"by,omitempty"`
	Created string     `yaml:"created" json:"created"`
	Updated string     `yaml:"updated,omitempty" json:"updated,omitempty"`
	Lifted  string     `yaml:"lifted,omitempty" json:"lifted,omitempty"`
	Log     []LogEntry `yaml:"log,omitempty" json:"log,omitempty"`
	Body    string     `yaml:"-" json:"body,omitempty"`
	File    string     `yaml:"-" json:"file"`
}

type Store struct {
	Sphere string
	Prefix string // ids are <Prefix>G-0001
	Root   string
	VCS    string
	By     string
	Now    func() time.Time
	Warn   func(string)
}

// OpenStore opens a configured sphere.
func OpenStore(cfg *config.Config, sphere, by string) (*Store, error) {
	s, ok := cfg.Spheres[sphere]
	if !ok {
		return nil, spec.UserError("unknown sphere %q; configured: %s. Example: guard init --sphere perso --root ~/guard/perso", sphere, strings.Join(cfg.Names(), ", "))
	}
	if _, err := os.Stat(s.Root); err != nil {
		return nil, spec.UserError("the rules of %s are missing at %s. Run: guard init --sphere %s --root %s", sphere, s.Root, sphere, s.Root)
	}
	if by == "" {
		by = "user"
	}
	return &Store{Sphere: sphere, Prefix: s.Prefix, Root: s.Root, VCS: s.VCS, By: by, Now: time.Now,
		Warn: func(m string) { fmt.Fprintln(os.Stderr, "guard: warning: "+m) }}, nil
}

// Init creates the store directory and, with a VCS, its repository.
func Init(root, vcs string) error {
	if err := os.MkdirAll(filepath.Join(root, ".guard"), 0o755); err != nil {
		return err
	}
	ignore := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(ignore); os.IsNotExist(err) {
		if err := os.WriteFile(ignore, []byte(".guard/\n"), 0o644); err != nil {
			return err
		}
	}
	switch vcs {
	case "jj":
		if _, err := os.Stat(filepath.Join(root, ".jj")); os.IsNotExist(err) {
			if out, err := runIn(root, "jj", "git", "init"); err != nil {
				return fmt.Errorf("jj git init: %v: %s", err, out)
			}
		}
	case "git":
		if _, err := os.Stat(filepath.Join(root, ".git")); os.IsNotExist(err) {
			if out, err := runIn(root, "git", "init", "-q"); err != nil {
				return fmt.Errorf("git init: %v: %s", err, out)
			}
		}
	}
	return nil
}

func runIn(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// WriteAs runs fn under the store's lock, then commits with the message it
// returns; an empty one commits nothing.
func (s *Store) WriteAs(fn func() (string, error)) error {
	if err := os.MkdirAll(filepath.Join(s.Root, ".guard"), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.Root, ".guard", "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return spec.Locked("cannot lock the rules of %s: %v", s.Sphere, err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	msg, err := fn()
	if err != nil {
		return err
	}
	if msg != "" {
		s.Commit(msg)
	}
	return nil
}

// Commit records the store's changes with a message.
func (s *Store) Commit(msg string) {
	var out string
	var err error
	switch s.VCS {
	case "jj":
		out, err = runIn(s.Root, "jj", "commit", "-m", msg)
	case "git":
		if out, err = runIn(s.Root, "git", "add", "-A"); err == nil {
			out, err = runIn(s.Root, "git", "commit", "-q", "--allow-empty", "-m", msg)
		}
	default:
		return
	}
	if err != nil && s.Warn != nil {
		s.Warn(fmt.Sprintf("%s commit failed in %s: %v: %s", s.VCS, s.Root, err, out))
	}
}

// Note appends a line to the rule's history and stamps it updated.
func (s *Store) Note(g *Guard, what string) {
	now := s.Now().Format(time.RFC3339)
	g.Log = append(g.Log, LogEntry{At: now, By: s.By, What: what})
	g.Updated = now
}

// SetState moves the rule to a state, with the date it was lifted.
func (s *Store) SetState(g *Guard, state string) {
	g.State = state
	if state == Lifted {
		g.Lifted = s.Now().Format(time.RFC3339)
	} else {
		g.Lifted = ""
	}
}

var numRe = regexp.MustCompile(`^(?:([A-Z]{1,3})G-)?0*(\d+)$`)

// NormID accepts PG-0007, pg-7 or 7 in the store of prefix P; an id of another sphere is refused.
func (s *Store) NormID(id string) (string, error) {
	m := numRe.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(id)))
	if m == nil || m[2] == "0" {
		return "", spec.UserError("rule id %q: expected %sG-0007 or 7", id, s.Prefix)
	}
	if m[1] != "" && m[1] != s.Prefix {
		return "", spec.UserError("rule %s is not in sphere %s, whose ids start with %sG-", strings.ToUpper(id), s.Sphere, s.Prefix)
	}
	n, _ := strconv.Atoi(m[2])
	return s.ID(n), nil
}

func (s *Store) ID(n int) string { return fmt.Sprintf("%sG-%04d", s.Prefix, n) }

func (s *Store) glob() string { return filepath.Join(s.Root, s.Prefix+"G-*.md") }

func (s *Store) path(id string) string { return filepath.Join(s.Root, id+".md") }

// List reads every rule, by id.
func (s *Store) List() ([]*Guard, error) {
	files, err := filepath.Glob(s.glob())
	if err != nil {
		return nil, err
	}
	var out []*Guard
	for _, f := range files {
		g, err := read(f)
		if err != nil {
			if s.Warn != nil {
				s.Warn(err.Error())
			}
			continue
		}
		out = append(out, g)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Get reads one rule.
func (s *Store) Get(id string) (*Guard, error) {
	id, err := s.NormID(id)
	if err != nil {
		return nil, err
	}
	g, err := read(s.path(id))
	if os.IsNotExist(err) {
		return nil, spec.NotFound("no rule %s in %s. List them with: guard ls --sphere %s --state all", id, s.Sphere, s.Sphere)
	}
	return g, err
}

// BySource finds the rule of a source, whatever its state.
func (s *Store) BySource(source string) (*Guard, error) {
	gs, err := s.List()
	if err != nil {
		return nil, err
	}
	for _, g := range gs {
		if g.Source == source {
			return g, nil
		}
	}
	return nil, nil
}

// NextID is one more than the highest id in the store.
func (s *Store) NextID() string {
	files, _ := filepath.Glob(s.glob())
	max := 0
	for _, f := range files {
		if m := numRe.FindStringSubmatch(strings.TrimSuffix(filepath.Base(f), ".md")); m != nil {
			if n, _ := strconv.Atoi(m[2]); n > max {
				max = n
			}
		}
	}
	return s.ID(max + 1)
}

// Save writes the rule's file atomically.
func (s *Store) Save(g *Guard) error {
	fm, err := yaml.Marshal(g)
	if err != nil {
		return err
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n")
	if body := strings.TrimSpace(g.Body); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	g.File = s.path(g.ID)
	tmp := g.File + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, g.File)
}

// Remove deletes the rule's file.
func (s *Store) Remove(id string) error { return os.Remove(s.path(id)) }

// Path is the file of a rule.
func (s *Store) Path(id string) string { return s.path(id) }

func read(path string) (*Guard, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	str := string(b)
	if !strings.HasPrefix(str, "---\n") {
		return nil, fmt.Errorf("%s: no front matter", path)
	}
	fm, body, ok := strings.Cut(str[4:], "\n---")
	if !ok {
		return nil, fmt.Errorf("%s: front matter not closed", path)
	}
	g := &Guard{}
	if err := yaml.Unmarshal([]byte(fm), g); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	g.Body = strings.TrimSpace(strings.TrimPrefix(body, "\n"))
	g.File = path
	if g.State == "" {
		g.State = Active
	}
	return g, nil
}
