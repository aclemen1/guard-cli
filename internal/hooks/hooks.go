// Package hooks runs the commands of the configuration on the events of a
// rule, so that other tools follow it without guard knowing them.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aclemen1/guard-cli/internal/config"
	"github.com/aclemen1/guard-cli/internal/store"
)

// Event is what a hook reads on its standard input.
type Event struct {
	Event  string       `json:"event"`
	Sphere string       `json:"sphere"`
	By     string       `json:"by"`
	Key    string       `json:"key,omitempty"` // the fired key, for fired
	Guard  *store.Guard `json:"guard"`
}

func matches(h config.Hook, e Event) bool {
	if len(h.Events) > 0 {
		ok := false
		for _, x := range h.Events {
			if x == e.Event {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	if h.Sphere != "" && h.Sphere != e.Sphere {
		return false
	}
	if h.Ref != "" {
		ok := false
		for _, r := range e.Guard.Refs {
			if strings.HasPrefix(r, h.Ref) {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// Fire runs every matching hook, one after the other; a failure is a warning, never an error of the action.
func Fire(cfg *config.Config, e Event, warn func(string)) {
	for _, h := range cfg.Hooks {
		if !matches(h, e) || len(h.Run) == 0 {
			continue
		}
		if err := run(h, e); err != nil && warn != nil {
			warn(fmt.Sprintf("hook %s on %s %s: %v", h.Run[0], e.Event, e.Guard.ID, err))
		}
	}
}

func run(h config.Hook, e Event) error {
	in, _ := json.Marshal(e)
	env := []string{"GUARD_EVENT=" + e.Event, "GUARD_ID=" + e.Guard.ID, "GUARD_SPHERE=" + e.Sphere,
		"GUARD_STATE=" + e.Guard.State, "GUARD_REFS=" + strings.Join(e.Guard.Refs, ","),
		"GUARD_TRIGGER=" + e.Guard.Trigger, "GUARD_KEY=" + e.Key}
	_, err := Run(h.Run, h.Timeout, in, env)
	return err
}

// Run runs a configured command with JSON on stdin and returns what it printed.
func Run(argv []string, timeout string, stdin []byte, env []string) (string, error) {
	d := 30 * time.Second
	if timeout != "" {
		t, err := time.ParseDuration(timeout)
		if err != nil {
			return "", fmt.Errorf("timeout %q: %v", timeout, err)
		}
		d = t
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	args := make([]string, len(argv))
	for i, a := range argv {
		args[i] = config.Expand(a)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = append(os.Environ(), env...)
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
