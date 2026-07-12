# Sections — user-defined snippet store

**Date:** 2026-07-12
**Status:** Approved, ready for implementation planning

## Summary

Add a second store to clipse: user-created, named sections ("categories") holding
text snippets the user deliberately saves. It sits alongside the clipboard history
and is independent of it.

The clipboard history is automatic and ephemeral — it self-trims at `maxHistory`
(100 by default), evicting the oldest unpinned entry on every new copy. Pinning
protects an entry from that eviction, but the entry still lives in the churning
history list. Sections are the opposite: nothing is added without the user asking,
and nothing is ever evicted.

Pinning, the history, and the clipboard listener are unchanged by this feature.

## Non-goals (v1)

- Images in sections (text only)
- Macros (noted as a possible later version)
- Renaming sections
- CLI flags for sections (TUI only)
- Any change to pinning or history semantics

## Storage

New file: `<config-dir>/sections.json`, sibling to `clipboard_history.json`.

```json
{
  "sections": [
    {
      "name": "Emails",
      "created": "2026-07-12 14:40:37.261642926",
      "items": [
        {
          "value": "someone@example.com",
          "added": "2026-07-12 14:41:02.112000000"
        }
      ]
    }
  ]
}
```

Go types (`config/sections.go`):

```go
type SectionItem struct {
    Value string `json:"value"`
    Added string `json:"added"`
}

type Section struct {
    Name    string        `json:"name"`
    Created string        `json:"created"`
    Items   []SectionItem `json:"items"`
}

type Sections struct {
    Sections []Section `json:"sections"`
}
```

### Why a separate file

1. **It is outside the listener's write race.** The clipboard listener rewrites
   `clipboard_history.json` in full on every copy event (`WriteUpdate` marshals the
   whole array and calls `os.WriteFile`), with no locking. The TUI writes the same
   file. A pin toggle racing a copy event can silently clobber one of the two
   writes. Curated, hand-saved data must not live in that blast radius.
   `sections.json` is written only by the TUI.
2. **The schemas do not overlap.** A `ClipboardItem` carries `recorded`,
   `filePath`, and `pinned`. A section item has none of those meanings. Reusing the
   type would mean a struct full of dead fields.

### Identity

- A **section** is identified by `name`. Names are unique. Empty names and
  duplicate names are rejected at the point of creation.
- A **section item** is identified by its `added` timestamp — the same convention
  the history file already uses (`recorded` is the de-facto ID there, used by
  `DeleteItems` and `TogglePinClipboardItem`). Duplicate values within a section
  are permitted.

### Writes

Writes are atomic: marshal, write to `sections.json.tmp`, `os.Rename` into place.
This differs deliberately from `config.WriteUpdate`, which writes in place. The
history file is disposable — a torn write costs you some clipboard history. The
sections file is the permanent, hand-curated store; a crash mid-write must not be
able to destroy it.

Disk space is checked before writing via `utils.DiskspaceAvailable`, matching
`WriteUpdate`.

### Failure handling

- **File missing** — created empty on init, matching `initHistoryFile`.
- **File corrupt / unparseable** — log the error and start with an empty section
  list. The TUI must not crash on a bad sections file, and must not overwrite it
  until the user makes an explicit change.
- **Duplicate or empty section name** — rejected, surfaced to the user as a list
  status message. No write occurs.

### Interaction with existing commands

`clipse -clear`, `-clear-all`, `-clear-images`, `-clear-text`, and the
`deleteAfter` expiry all operate on the history file only. None of them touch
sections. This is by construction, not by special-casing: they call
`config.WriteUpdate` against `HistoryFilePath`.

## Architecture

### The problem being avoided

The existing TUI tracks view-modes with boolean flags on a single `Model`
(`showPreview`, `showConfirmation`, `togglePinned`). Each mode transition manually
enables/disables ~16 keybindings; `setPreviewKeys()` and `setConfirmationKeys()`
are near-duplicate 15-line functions doing exactly that. `View()` switches on those
booleans. `Update()` is already 744 lines.

This feature introduces five more modes (section list, section contents, name
prompt, value prompt, history picker). Adding `showSections` / `showSectionItems` /
`showInput` / `showPicker` booleans to the same model would produce a multi-way
truth table in `Update()`, four more `setXKeys()` clones, and a serious risk of
regressing the clipboard manager in daily use.

### The approach

Sections live in a **self-contained Bubble Tea sub-model**. The root `Model` gains
a single `screen` field:

```go
type screen int

const (
    screenHistory screen = iota
    screenSections
)
```

- When `screen == screenHistory`, behavior is byte-for-byte what it is today.
- When `screen == screenSections`, the root `Update()` and `View()` delegate
  wholly to the sections sub-model.

The root model is modified in exactly two places: one new key case (open sections)
and one delegation branch. The existing `showX` / `setXKeys()` machinery is not
extended.

### New files

| File | Responsibility |
|---|---|
| `config/sections.go` | Load/save `sections.json`; add/delete section; add/delete item. Pure file I/O, no TUI types. |
| `app/sections.go` | The sections sub-model: its own `list.Model`, keymap, internal state, `Update`, `View`. |

The sub-model owns its internal states (section list / section contents / text
input / history picker) privately. They are not visible to, and cannot interfere
with, the root model's state.

### Reused components

- `bubbles/list` for both the section list and the section-contents list, giving
  fuzzy filter (`/`), pagination, and multi-select for free.
- `bubbles/textinput` for the name and value prompts. Already available via the
  `bubbles` dependency — no new module.
- The `newConfirmationList` *constructor* for destructive section deletion. The
  sub-model builds and owns its own instance; it does not share the root model's
  `confirmationList`, so the two cannot interfere.
- `display.DisplayServer.CopyText` for all copies, so Wayland/X11/macOS all work
  through the same abstraction already in place.

### Returning to the history view

The root model's state is left intact while the sections screen is active, so
returning restores the history list as it was (cursor position, pinned toggle).
Any active filter on the history list is reset before switching screens, so the
user does not return to a filtered view they have forgotten about.

## Screens and behavior

### Section list

Entered from the history view. Empty on first use.

- **add** — prompts for a name; on submit, creates the section and drops the user
  straight inside it (per the original request)
- **delete** — routed through the existing confirmation screen, because deleting a
  section takes its items with it
- **copy** — yanks the whole section: all item values joined by newlines. This
  mirrors the existing multi-select yank behavior in the history list, and makes a
  section usable as a single block.
- **enter** — drill into the section
- **esc** — back to the history view

### Inside a section

- **add** — type a value by hand (text input)
- **import** — opens a picker over the **full** clipboard history, with `/` fuzzy
  filter and `tab` to collapse to pinned-only, multi-select supported. Selected
  entries are **snapshotted** into the section: the value is copied, with a fresh
  `added` timestamp, and no reference back to the history entry is retained.
- **delete** — remove an item
- **copy** — copy the item to the clipboard and stay in the TUI
- **enter** — copy the item and exit, identical to `choose` in the history list, so
  the existing auto-paste path (`shell.RunAutoPaste`) continues to work unchanged
- **esc** — back to the section list

### Why import shows the full history, not pinned-only

Restricting the picker to pinned items would force the user to pin something purely
in order to save it — double work — and would overload what `pinned` means. Today
`pinned: true` means exactly one thing: "do not evict this at `maxHistory`". Making
the picker pinned-only would silently turn it into a second thing: "staging area for
sections". Those meanings drift apart and confuse.

The objection to showing all 100 entries is really an objection to *scrolling*, and
the list already solves that: `/` fuzzy-filters, and `tab` gives the pinned-only view
on demand. The user gets both views without either meaning being compromised.

### Why imports are snapshots

A section item must not depend on the history entry it came from. The history evicts
entries at `maxHistory` and deletes them; a linked section item would either die with
it or become a dangling reference. Sections are the permanent store. "Save" must
actually mean save.

## Keybindings

All new bindings are registered in `config.defaultKeyBindings()`, so they are
user-configurable like every existing key.

**No migration is required for existing users.** `config.ClipseConfig` is
initialized to `defaultConfig()` *before* `json.Unmarshal` reads the user's
`config.json`, and Go's `encoding/json` **merges** into a non-nil map rather than
replacing it — keys present in the defaults but absent from the user's file are
retained. New bindings therefore apply to existing installs without rewriting
`config.json`.

Proposed defaults (the specific characters are not load-bearing; the user has stated
no preference and reuse of the same character across different screens is fine, since
each screen owns its own keymap):

| Config key | Default | Screen | Action |
|---|---|---|---|
| `sections` | `W` | history | open the section list |
| `sectionAdd` | `a` | both | add section / add item |
| `sectionDelete` | `d` | both | delete section / delete item |
| `sectionCopy` | `c` | both | copy section / copy item |
| `sectionImport` | `i` | section contents | import from history |

Navigation (`up`, `down`, `choose`, `quit`) reuses the existing config keys.

**Filter guard:** the section lists support `/` filtering, during which letter keys
must type into the filter rather than trigger actions. All action keys are gated on
`!list.SettingFilter()`, the same guard the history list already uses.

## Testing

`config/sections.go` is pure file I/O and tests cleanly without a TTY. Unit tests in
`tests/config/sections_test.go`, alongside the existing history tests:

- create section; reject empty name; reject duplicate name
- add item; delete item; delete section (removes its items)
- persistence round-trip: write, re-read, deep-equal
- corrupt file → empty sections, no panic, file not clobbered
- missing file → created empty

The existing test suite must continue to pass, verifying the history path is
unregressed.

Manual verification on Wayland: build with `make wayland`, run against an isolated
`XDG_CONFIG_HOME` so the developer's real clipse instance and history are untouched.
