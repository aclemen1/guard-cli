# guard

Rules of conduct tied to an object, recalled when their situation comes:
« never sign the debtor's commitment », « nothing agreed without the insurer ».
A rule calls for no action; it forbids or conditions one. One binary: CLI, MCP
server (`guard mcp`), TUI (`guard tui`) and agent skill (`guard skill install`).

```bash
go install ./cmd/guard
guard init --sphere home --root ~/guard/home
guard add "Never sign the debtor's commitment" --sphere home --ref case:deposit \
  --trigger with:contact:AGENCY --when "when the agency sends a paper to sign"
guard ls --ref case:deposit --format text
guard fire with:contact:AGENCY
guard schema
```

## Store

One Markdown file per rule in `<root>/<prefix>G-0001.md`: YAML front matter for
the fields, body for the context. Each change is committed (jj by default)
under a lock in `<root>/.guard/lock`.

| Field | Meaning |
|---|---|
| `title` | the rule itself |
| `state` | `active` or `lifted` |
| `refs` | what the rule belongs to, as `<tool>:<id>`; several |
| `trigger` | key that recalls it: `on:<tool>:<event>[:<id>]` or `with:<…>`; optional |
| `when` | the situation in words |
| `source` | origin, as `<tool>:<id>`; one rule per source and sphere |
| `by`, `created`, `updated`, `lifted`, `log` | author, dates, history |

## Triggers

Keys follow `github.com/aclemen1/trigger`. A trigger matches a fired key when
its segments start the key's: `on:mail:reply` matches
`on:mail:reply:thread/abc`; `on:mail:rep` does not. `guard fire <key>` returns
the active rules that match and runs the hooks of `fired`; `--dry-run` only
lists. `guard ls --trigger with:` gives the rules of a situation, for whoever
compares them with a day's agenda.

## Hooks

A hook runs a command after an event of a rule. The event comes as JSON on
stdin (`event`, `sphere`, `by`, `key`, `guard`) and in the environment
(`GUARD_EVENT`, `GUARD_ID`, `GUARD_SPHERE`, `GUARD_STATE`, `GUARD_REFS`,
`GUARD_TRIGGER`, `GUARD_KEY`). Events: `added`, `edited`, `lifted`, `restored`,
`removed`, `fired`. An action that changes nothing commits and fires nothing.

```yaml
hooks:
  - events: [fired]
    ref: "case:"               # rules with a ref that starts with this
    run: ["~/bin/tell-case"]   # argv; a failure is a warning, never an error
```

## Notes and links

guard keeps no notes. `guard note <id> "<text>"` (key `N` in the TUI) runs
`notes.run` with `{"sphere","guard","text"}` on stdin. Key `o` runs `open.run`
with `{"sphere","guard"}` to open what the rule refers to.

## TUI

`guard tui`: views 1 Actives, 2 Levées, 3 Toutes; columns ID, Règle, Quand,
Réf.; the detail of the selected rule. `guard tui --select PG-0001` opens on
that rule, in the view that holds it; an unknown id opens as usual, with a
message. Rules come in sections by state and sphere: `←` folds a section, or
climbs from a rule to its header; `→` unfolds it, or opens the rule's card.
`c` new, `E` edit, `e` and `x` lift,
`space` lift or restore, `#` delete, `N` note, `o` open, `s` sphere, `/`
filter, `t T` sort, `r` reload, `?` keys. Every input is a modal (tuikit). It
reloads when a rule file changes or on SIGUSR1, and restarts itself on a new
binary.

## Configuration

`~/.config/guard/config.yaml` (or `$GUARD_CONFIG`, `--config`):

```yaml
spheres:
  home: {root: ~/guard/home, prefix: H, vcs: jj}
  work: {root: ~/guard/work, prefix: W, vcs: jj}
hooks: []
notes: {run: ["~/bin/add-note"]}
open: {run: ["~/bin/open-ref"]}
complete: {refs: [cases, people]}   # sources of tuikit's shared completion (refs.yaml), by field
```

`history.ls` is an argv that prints the latest entries of a rule's journal;
`{id}` and `{sphere}` name the rule. `guard show` (field `history`; `journal` too, for a while) and the
card of the TUI show what it prints; nothing is shown when it prints nothing
or fails.

```yaml
history: {ls: [my-journal, ls, "rule:{id}", --limit, "10"], timeout: 5s}
```

The Réfs field of the TUI proposes the refs already cited by rules, then those
of the sources named in `complete.refs` (formerly `refs`), declared once in `~/.config/tuikit/refs.yaml`
(see tuikit's `complete` package).

| Variable | Effect |
|---|---|
| `GUARD_SPHERE` | sphere of reads on the command line |
| `GUARD_BY` | author written in the history (`agent:…` for an agent) |
