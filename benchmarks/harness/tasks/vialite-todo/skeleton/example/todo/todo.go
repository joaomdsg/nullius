// Package todo is the collaborative shared todo list — the deliverable.
//
// STATUS: STUB. The composition, its fields, and its action set are
// unfinished. Complete them so the app behaves as the task spec requires
// and the smoke test in todo_test.go passes. You own the via-lite
// framework too (module root): if a behavior the app needs is broken or
// missing there, fix it at the root — do not paper over it here.
package todo

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Item is one todo. The shared list is a slice of these.
type Item struct {
	ID   int
	Text string
	Done bool
}

// Todos is the shared, multi-user list. All connected clients see the same
// items live; the draft input is private to each tab.
//
// TODO: wire the fields. The shared list must be app-scoped so every
// session converges on it; the draft must be a per-tab client signal.
type Todos struct {
	// Draft via.SignalStr        `via:"draft"`
	// Sel   via.SignalNum[int]   `via:"sel"`
	// Items via.StateAppSlice[Item] `via:"items"`
}

// Add appends the current draft as a new item, then clears the draft.
// TODO: implement.
func (t *Todos) Add(ctx *via.Ctx) error { return nil }

// Toggle flips the Done flag of the item whose ID matches the "sel" signal.
// TODO: implement.
func (t *Todos) Toggle(ctx *via.Ctx) error { return nil }

// Delete removes the item whose ID matches the "sel" signal.
// TODO: implement.
func (t *Todos) Delete(ctx *via.Ctx) error { return nil }

// View renders the input, an add button, and the shared list.
// TODO: render the real UI. The list <ul> must have id="list" and each
// row must show the item text.
func (t *Todos) View(ctx *via.CtxR) h.H {
	return h.Div(h.P(h.Text("TODO: build the todo app")))
}
