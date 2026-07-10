package todo_test

// Visible smoke test — the acceptance floor for the todo app. It proves the
// happy path only: a single user can add an item and see it rendered, and a
// second connected client receives it live. It deliberately does NOT probe
// concurrency, isolation, delivery-under-failure, or lifecycle — those are
// the framework's responsibility and are graded separately. Making this pass
// is necessary, not sufficient.

import (
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/example/todo"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTodoSmoke_addRendersItem(t *testing.T) {
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[todo.Todos](app, "/")

	c := vt.NewClient(t, server, "/")
	require.Equal(t, 200, c.Action("Add").WithSignal("draft", "buy milk").Fire())

	assert.Contains(t, c.Reload(), "buy milk",
		"after Add, the shared list must render the new item")
}

func TestTodoSmoke_addDeliversToOtherClientLive(t *testing.T) {
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[todo.Todos](app, "/")

	a := vt.NewClient(t, server, "/")
	b := vt.NewClient(t, server, "/")
	frames, cancel := b.SSEReady()
	defer cancel()

	require.Equal(t, 200, a.Action("Add").WithSignal("draft", "walk dog").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "walk dog")
}
