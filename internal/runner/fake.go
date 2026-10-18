package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ErrNotFound is returned by Fake.LookPath for tools not registered as
// installed.
var ErrNotFound = errors.New("executable file not found in $PATH")

// Call records one invocation made through a Fake.
type Call struct {
	Dir  string
	Name string
	Args []string
}

// String renders the call as a shell-like command line.
func (c Call) String() string {
	return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " "))
}

// Fake is a scripted Runner for tests. Responses are matched by prefix
// against the rendered command line; the longest matching prefix wins.
type Fake struct {
	mu        sync.Mutex
	responses map[string]Result
	errs      map[string]error
	funcs     map[string]func(Call) (Result, error)
	installed map[string]bool
	calls     []Call
}

// NewFake returns a Fake where every tool in installed is found by LookPath.
func NewFake(installed ...string) *Fake {
	f := &Fake{
		responses: map[string]Result{},
		errs:      map[string]error{},
		funcs:     map[string]func(Call) (Result, error){},
		installed: map[string]bool{},
	}
	for _, name := range installed {
		f.installed[name] = true
	}
	return f
}

// On registers the result for commands starting with prefix.
func (f *Fake) On(prefix string, res Result) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[prefix] = res
	return f
}

// OnError registers a start error for commands starting with prefix.
func (f *Fake) OnError(prefix string, err error) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[prefix] = err
	return f
}

// OnFunc registers a function computing the result for commands starting
// with prefix, for fakes that must also produce side effects such as
// writing a coverage profile. Functions take precedence over On and
// OnError.
func (f *Fake) OnFunc(prefix string, fn func(Call) (Result, error)) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.funcs[prefix] = fn
	return f
}

// Install marks tools as found by LookPath.
func (f *Fake) Install(names ...string) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, n := range names {
		f.installed[n] = true
	}
	return f
}

// Calls returns every call made so far.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Ran reports whether any call's command line starts with prefix.
func (f *Fake) Ran(prefix string) bool {
	for _, c := range f.Calls() {
		if strings.HasPrefix(c.String(), prefix) {
			return true
		}
	}
	return false
}

// Run implements Runner.
func (f *Fake) Run(ctx context.Context, dir, name string, args ...string) (Result, error) {
	f.mu.Lock()
	call := Call{Dir: dir, Name: name, Args: args}
	f.calls = append(f.calls, call)
	line := call.String()
	fn, hasFn := longest(f.funcs, line)
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if hasFn {
		return fn(call)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := longest(f.errs, line); ok {
		return Result{}, err
	}
	if res, ok := longest(f.responses, line); ok {
		return res, nil
	}
	return Result{}, nil
}

// LookPath implements Runner.
func (f *Fake) LookPath(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.installed[name] {
		return "/usr/bin/" + name, nil
	}
	return "", fmt.Errorf("%s: %w", name, ErrNotFound)
}

func longest[V any](m map[string]V, line string) (V, bool) {
	var (
		best  V
		found bool
		size  = -1
	)
	for prefix, v := range m {
		if strings.HasPrefix(line, prefix) && len(prefix) > size {
			best, found, size = v, true, len(prefix)
		}
	}
	return best, found
}
