package cli

import (
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/spf13/cobra"
)

// fakePrompter is prompter driven from a fixed, ordered script of answers a
// test sets up in advance. Each answer is tagged with a substring the next
// prompt's title must contain. It fails the test on a prompt it did not
// expect (kind or title mismatch, or none scripted), and useFakePrompter
// fails it at cleanup over answers the test never asked for.
type fakePrompter struct {
	t           *testing.T
	interactive bool
	answers     []fakeAnswer
	// asked records every prompt kind and title asked, in order, for a test
	// that only cares whether (and how many times) it was asked at all.
	asked []string
	// confirmDefaults records the defaultYes each Confirm call received, in
	// order, for a test that checks a prompt defaults to No.
	confirmDefaults []bool
}

// fakeAnswer is one scripted answer: the substring its prompt's title must
// contain, which prompter method it answers, and the value or error that
// method returns.
type fakeAnswer struct {
	title string
	kind  string
	value any
	err   error
}

func confirmAnswer(title string, ok bool) fakeAnswer {
	return fakeAnswer{title: title, kind: "Confirm", value: ok}
}

func selectAnswer(title, key string) fakeAnswer {
	return fakeAnswer{title: title, kind: "Select", value: key}
}

func multiSelectAnswer(title string, keys []string) fakeAnswer {
	return fakeAnswer{title: title, kind: "MultiSelect", value: keys}
}

func groupedMultiSelectAnswer(title string, keys []string) fakeAnswer {
	return fakeAnswer{title: title, kind: "GroupedMultiSelect", value: keys}
}

func inputAnswer(title, text string) fakeAnswer {
	return fakeAnswer{title: title, kind: "Input", value: text}
}

// useFakePrompter installs p in place of newPrompter's production adapter for
// the duration of the test, and fails the test at cleanup if any scripted
// answer went unused.
func useFakePrompter(t *testing.T, p *fakePrompter) {
	t.Helper()
	p.t = t
	old := newPrompter
	newPrompter = func(*cobra.Command) prompter { return p }
	t.Cleanup(func() {
		newPrompter = old
		if len(p.answers) > 0 {
			t.Fatalf("%d scripted prompt answer(s) went unused", len(p.answers))
		}
	})
}

func (p *fakePrompter) Interactive() bool { return p.interactive }

// next consumes the next scripted answer, failing the test if kind is not
// what was expected next, or if title does not contain the answer's tagged
// substring.
func (p *fakePrompter) next(kind, title string) fakeAnswer {
	p.t.Helper()
	p.asked = append(p.asked, kind+": "+title)
	if len(p.answers) == 0 {
		p.t.Fatalf("unexpected %s prompt %q: no scripted answers left", kind, title)
	}
	a := p.answers[0]
	p.answers = p.answers[1:]
	if a.kind != kind {
		p.t.Fatalf("prompt %q is a %s prompt; the next scripted answer is for %s %q", title, kind, a.kind, a.title)
	}
	if !strings.Contains(title, a.title) {
		p.t.Fatalf("%s prompt title = %q; want it to contain %q", kind, title, a.title)
	}
	return a
}

func (p *fakePrompter) Confirm(title string, defaultYes bool) (bool, error) {
	p.confirmDefaults = append(p.confirmDefaults, defaultYes)
	a := p.next("Confirm", title)
	if a.err != nil {
		return false, a.err
	}
	ok, _ := a.value.(bool)
	return ok, nil
}

func (p *fakePrompter) Select(title string, _ []tui.SelectOption, _ int) (string, error) {
	a := p.next("Select", title)
	if a.err != nil {
		return "", a.err
	}
	key, _ := a.value.(string)
	return key, nil
}

func (p *fakePrompter) MultiSelect(title string, _ []tui.SelectOption) ([]string, error) {
	a := p.next("MultiSelect", title)
	if a.err != nil {
		return nil, a.err
	}
	keys, _ := a.value.([]string)
	return keys, nil
}

func (p *fakePrompter) GroupedMultiSelect(title string, _ tui.GroupedItems, _ []string) ([]string, error) {
	a := p.next("GroupedMultiSelect", title)
	if a.err != nil {
		return nil, a.err
	}
	keys, _ := a.value.([]string)
	return keys, nil
}

func (p *fakePrompter) Input(title string) (string, error) {
	a := p.next("Input", title)
	if a.err != nil {
		return "", a.err
	}
	text, _ := a.value.(string)
	return text, nil
}
