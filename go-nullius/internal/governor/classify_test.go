package governor

import "testing"

func TestGate(t *testing.T) {
	cases := []struct {
		cmd   string
		allow bool
	}{
		{"go test ./...", false},
		{"go build ./... 2>&1 | tail -20", true}, // bounded heavy is allowed
		{"golangci-lint run", false},
		{"npm test", false},
		{"git status", true},
		{"ls -la internal/", true},
		{"grep -rn TODO .", false},
		{"grep -rn TODO internal/governor/classify.go", true}, // concrete target
		{"rg 'func Gate'", false},
		{"rg 'func Gate' internal/governor", true},
		{"grep -c foo main.go", true},
		{"go test ./... | tail -5 | grep something", false}, // cap not final segment
	}
	for _, c := range cases {
		v := Gate(c.cmd)
		if v.Allow != c.allow {
			t.Errorf("Gate(%q).Allow = %v, want %v (reason %q)", c.cmd, v.Allow, c.allow, v.Reason)
		}
	}
}

func TestIsBounded(t *testing.T) {
	if IsBounded("go test ./...") {
		t.Error("unbounded command reported bounded")
	}
	if !IsBounded("go test ./... 2>&1 | tail -n 30") {
		t.Error("tail-capped command reported unbounded")
	}
	if IsBounded("go test | tail -5 | cat") {
		t.Error("cap followed by another pipe must not count as bounded")
	}
}
