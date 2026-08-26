package devlog

import (
	"fmt"
	"strings"
	"testing"
)

func TestRingEvictsOldestAndOrdersNewestFirst(t *testing.T) {
	l := New()
	for i := 0; i < Capacity+10; i++ {
		l.Add(Entry{Model: fmt.Sprintf("m%d", i)})
	}
	entries := l.Entries()
	if len(entries) != Capacity {
		t.Fatalf("got %d entries, want %d", len(entries), Capacity)
	}
	if entries[0].Model != fmt.Sprintf("m%d", Capacity+9) {
		t.Errorf("newest first violated: %+v", entries[0])
	}
	if entries[len(entries)-1].Model != "m10" {
		t.Errorf("oldest surviving entry wrong: %+v", entries[len(entries)-1])
	}
}

func TestResponseCapped(t *testing.T) {
	l := New()
	l.Add(Entry{Response: strings.Repeat("x", maxResponse+100)})
	e := l.Entries()[0]
	if len(e.Response) != maxResponse || !e.Truncated {
		t.Errorf("response not capped: len=%d truncated=%v", len(e.Response), e.Truncated)
	}
}

func TestClear(t *testing.T) {
	l := New()
	l.Add(Entry{})
	l.Clear()
	if len(l.Entries()) != 0 {
		t.Error("clear left entries behind")
	}
}
