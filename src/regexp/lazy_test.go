// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package regexp

import (
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

// lazy returns re as an init image holds it: without its programs.
func lazy(re *Regexp) *Regexp {
	l := *re
	l.prog, l.onepass, l.lazy = nil, nil, new(lazyRegexp)
	return &l
}

func TestLazy(t *testing.T) {
	for _, compile := range []func(string) (*Regexp, error){Compile, CompilePOSIX} {
		for _, test := range findTests {
			eager, err := compile(test.pat)
			if err != nil {
				continue
			}
			l := lazy(eager)
			if !reflect.DeepEqual(l.compiled().prog, eager.prog) {
				t.Errorf("%s: lazy program differs", test)
			}
			check := func(what string, a, b any) {
				if !reflect.DeepEqual(a, b) {
					t.Errorf("%s: %s: lazy %v, eager %v", test, what, a, b)
				}
			}
			l = lazy(eager)
			check("MatchString", l.MatchString(test.text), eager.MatchString(test.text))
			l = lazy(eager)
			check("FindAllStringSubmatchIndex", l.FindAllStringSubmatchIndex(test.text, -1), eager.FindAllStringSubmatchIndex(test.text, -1))
			l = lazy(eager)
			check("FindSubmatch", l.FindSubmatch([]byte(test.text)), eager.FindSubmatch([]byte(test.text)))
			l = lazy(eager)
			check("FindReaderSubmatchIndex", l.FindReaderSubmatchIndex(strings.NewReader(test.text)), eager.FindReaderSubmatchIndex(strings.NewReader(test.text)))
			l = lazy(eager)
			check("ReplaceAllString", l.ReplaceAllString(test.text, "[$0$1]"), eager.ReplaceAllString(test.text, "[$0$1]"))
			l = lazy(eager)
			check("AllStringSubmatch", slices.Collect(l.AllStringSubmatch(test.text)), slices.Collect(eager.AllStringSubmatch(test.text)))
			l = lazy(eager)
			check("Split", l.Split(test.text, -1), eager.Split(test.text, -1))
			l = lazy(eager)
			check("NumSubexp", l.NumSubexp(), eager.NumSubexp())
			check("SubexpNames", l.SubexpNames(), eager.SubexpNames())
			check("String", l.String(), eager.String())
			if l.prog != nil || l.lazy.re != nil {
				t.Errorf("%s: accessors compiled the programs", test)
			}
		}
	}
}

func TestLazyLongest(t *testing.T) {
	l := lazy(MustCompile(`a(|b)`))
	c := l.Copy()
	l.Longest()
	if l.lazy != nil || l.prog == nil {
		t.Fatal("Longest left the regexp lazy")
	}
	if got := l.FindString("ab"); got != "ab" {
		t.Errorf("longest: got %q, want %q", got, "ab")
	}
	if got := c.FindString("ab"); got != "a" {
		t.Errorf("copy: got %q, want %q", got, "a")
	}
}

func TestLazyConcurrent(t *testing.T) {
	l := lazy(MustCompile(`(\w+)@(\w+)\.com`))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if m := l.FindStringSubmatch("x bob@example.com y"); len(m) != 3 || m[2] != "example" {
				t.Errorf("got %q", m)
			}
		})
	}
	wg.Wait()
}
