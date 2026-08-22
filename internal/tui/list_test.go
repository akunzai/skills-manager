package tui

import (
	"bytes"
	"io"
	"testing"

	"github.com/akunzai/skills-manager/internal/presentation"
)

func scriptedKeys(keys ...keyType) func() keyType {
	i := 0
	return func() keyType {
		if i >= len(keys) {
			return keyEscape
		}
		k := keys[i]
		i++
		return k
	}
}

func testViewport() promptSize {
	return sizeFromViewport(promptViewport(80, 24))
}

func TestRunSelectEnterPicksDefault(t *testing.T) {
	options := []SelectOption{{Key: "a", Title: "A"}, {Key: "b", Title: "B"}}
	got, err := runSelect("pick", options, 0, presentation.Style{}, io.Discard, scriptedKeys(keyEnter), testViewport())
	if err != nil {
		t.Fatal(err)
	}
	if got != "a" {
		t.Fatalf("got %q; want a", got)
	}
}

func TestRunSelectDownEnterPicksSecond(t *testing.T) {
	options := []SelectOption{{Key: "a"}, {Key: "b"}}
	got, err := runSelect("pick", options, 0, presentation.Style{}, io.Discard, scriptedKeys(keyDown, keyEnter), testViewport())
	if err != nil {
		t.Fatal(err)
	}
	if got != "b" {
		t.Fatalf("got %q; want b", got)
	}
}

func TestRunSelectEscapeCancels(t *testing.T) {
	options := []SelectOption{{Key: "a"}}
	got, err := runSelect("pick", options, 0, presentation.Style{}, io.Discard, scriptedKeys(keyEscape), testViewport())
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("got %q; want empty cancel", got)
	}
}

func TestRunMultiSelectSpaceAndEnter(t *testing.T) {
	items := []SelectOption{{Key: "one"}, {Key: "two", Selected: true}}
	got, err := runMultiSelect("pick", items, presentation.Style{}, io.Discard, scriptedKeys(keySpace, keyEnter), testViewport())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %#v; want one and two", got)
	}
}

func TestRunMultiSelectEscapeReturnsNil(t *testing.T) {
	items := []SelectOption{{Key: "one"}}
	got, err := runMultiSelect("pick", items, presentation.Style{}, io.Discard, scriptedKeys(keyEscape), testViewport())
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("got %#v; want nil cancel", got)
	}
}

func TestRunGroupedSpaceOnHeaderSelectsGroup(t *testing.T) {
	groups := GroupedItems{
		"repo": {
			{Key: "alpha", Title: "alpha"},
			{Key: "beta", Title: "beta"},
		},
	}
	got, err := runGroupedMultiSelect("pick", groups, nil, presentation.Style{}, io.Discard, scriptedKeys(keySpace, keyEnter), testViewport())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %#v; want alpha and beta", got)
	}
}

func TestRunSelectRendersTitle(t *testing.T) {
	var buf bytes.Buffer
	options := []SelectOption{{Key: "a", Title: "Alpha"}}
	_, err := runSelect("Choose a scope:", options, 0, presentation.Style{}, &buf, scriptedKeys(keyEnter), testViewport())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("Choose a scope:")) {
		t.Fatalf("missing title in output: %q", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("Alpha")) {
		t.Fatalf("missing option in output: %q", buf.String())
	}
}
