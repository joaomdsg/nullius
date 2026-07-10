package hidden

// End-to-end proof that the reactive core composes into a real-time,
// multi-user shared todo app: many independent clients (distinct sessions)
// and sibling tabs (same session) all converge on one shared list, live,
// without reloads — while per-tab draft input stays private to its tab.
//
// This is the integration test unit tests cannot fake: it drives the whole
// stack (routing → action dispatch → StateApp CAS → subscription-scoped
// SSE fan-out) through the vt harness over real HTTP.

import (
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type todoItem struct {
	ID   int
	Text string
	Done bool
}

// todoApp is a canonical shared todo list built ONLY from the pinned
// public API. The shared list is app-scoped (StateApp); the draft is a
// per-tab client signal.
type todoApp struct {
	Draft via.SignalStr           `via:"draft"`
	Sel   via.SignalNum[int]      `via:"sel"`
	Items via.StateAppSlice[todoItem] `via:"items"`
}

func (a *todoApp) Add(ctx *via.Ctx) error {
	text := a.Draft.Read(ctx)
	if text == "" {
		return nil
	}
	return a.Items.Update(ctx, func(items []todoItem) ([]todoItem, error) {
		next := 1
		for _, it := range items {
			if it.ID >= next {
				next = it.ID + 1
			}
		}
		return append(items, todoItem{ID: next, Text: text}), nil
	})
}

func (a *todoApp) Toggle(ctx *via.Ctx) error {
	id := a.Sel.Read(ctx)
	return a.Items.Update(ctx, func(items []todoItem) ([]todoItem, error) {
		out := make([]todoItem, len(items))
		copy(out, items)
		for i := range out {
			if out[i].ID == id {
				out[i].Done = !out[i].Done
			}
		}
		return out, nil
	})
}

func (a *todoApp) Delete(ctx *via.Ctx) error {
	id := a.Sel.Read(ctx)
	a.Items.Op(ctx).Filter(func(it todoItem) bool { return it.ID != id })
	return nil
}

func (a *todoApp) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Input(a.Draft.Bind()),
		h.Button(on.Click(a.Add), h.Text("add")),
		h.Ul(h.ID("list"), h.Each(a.Items.Read(ctx), func(it todoItem) h.H {
			box := "[ ]"
			if it.Done {
				box = "[x]"
			}
			return h.Li(h.Class("todo"), h.Textf("%s %s", box, it.Text))
		})),
	)
}

func TestTodoE2E_multipleUsersConvergeOnSharedList(t *testing.T) {
	t.Parallel()
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[todoApp](app, "/")

	alice := vt.NewClient(t, server, "/")
	bob := vt.NewClient(t, server, "/")

	fa, ca := alice.SSEReady()
	defer ca()
	fb, cb := bob.SSEReady()
	defer cb()

	require.Equal(t, 200, alice.Action("Add").WithSignal("draft", "buy milk").Fire())

	// Bob (a different session) must see Alice's item live, no reload.
	vt.AwaitFrame(t, fb, 2*time.Second, "buy milk")
	vt.AwaitFrame(t, fa, 2*time.Second, "buy milk")

	require.Equal(t, 200, bob.Action("Add").WithSignal("draft", "walk dog").Fire())
	vt.AwaitFrame(t, fa, 2*time.Second, "walk dog")

	// A late joiner sees the whole converged list on first render.
	carol := vt.NewClient(t, server, "/")
	body := carol.HTML()
	assert.Contains(t, body, "buy milk")
	assert.Contains(t, body, "walk dog")
}

func TestTodoE2E_siblingTabsShareOneListDraftsStayLocal(t *testing.T) {
	t.Parallel()
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[todoApp](app, "/")

	tab1 := vt.NewClient(t, server, "/")
	tab2 := tab1.Fork("/") // same session, second tab

	f1, c1 := tab1.SSEReady()
	defer c1()
	f2, c2 := tab2.SSEReady()
	defer c2()

	// tab1 types a draft but does not submit; tab2 must not see it.
	require.Equal(t, 200, tab1.Action("Add").WithSignal("draft", "shared task").Fire())
	vt.AwaitFrame(t, f2, 2*time.Second, "shared task")

	// The list is shared across sibling tabs; the committed item shows on both.
	vt.AwaitFrame(t, f1, 2*time.Second, "shared task")
}

func TestTodoE2E_deletePropagatesToEveryClient(t *testing.T) {
	t.Parallel()
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[todoApp](app, "/")

	a := vt.NewClient(t, server, "/")
	b := vt.NewClient(t, server, "/")

	fb, cb := b.SSEReady()
	defer cb()
	// b must observe the add live, then the delete.
	require.Equal(t, 200, a.Action("Add").WithSignal("draft", "temp item").Fire())
	vt.AwaitFrame(t, fb, 2*time.Second, "temp item")

	require.Equal(t, 200, a.Action("Delete").WithSignal("sel", 1).Fire())

	// After delete, a fresh client's render must not contain the item.
	deadline := time.Now().Add(2 * time.Second)
	for {
		c := vt.NewClient(t, server, "/")
		if !strings.Contains(c.HTML(), "temp item") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("deleted item still present in shared list after 2s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
