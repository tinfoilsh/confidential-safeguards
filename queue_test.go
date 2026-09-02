package main

import (
	"testing"
	"time"
)

func TestQueue_NewTurnReplacesPrefix(t *testing.T) {
	q := NewQueue(time.Hour, 100)
	first := mustConversation(t, "u1", "["+turnOne+"]")
	other := mustConversation(t, "u2", "["+turnOne+"]")
	second := mustConversation(t, "u1", "["+turnTwo+"]")

	q.Push(first)
	q.Push(other)
	q.Push(second)

	if q.Len() != 2 {
		t.Fatalf("len = %d, want 2", q.Len())
	}
	if got := q.Pop(); got != other {
		t.Fatal("unrelated conversation should now be at the front")
	}
	if got := q.Pop(); got != second {
		t.Fatal("extended conversation should replace its prefix at the back")
	}
	if q.Pop() != nil {
		t.Fatal("queue should be empty")
	}
}

func TestQueue_DuplicateMovesToBack(t *testing.T) {
	q := NewQueue(time.Hour, 100)
	a := mustConversation(t, "u1", "["+turnOne+"]")
	b := mustConversation(t, "u2", "["+turnOne+"]")

	q.Push(a)
	q.Push(b)
	q.Push(mustConversation(t, "u1", "["+turnOne+"]"))

	if q.Len() != 2 || q.Pop() != b {
		t.Fatal("re-pushed conversation should move to the back without duplicating")
	}
}

func TestQueue_StaleTurnArrivingLateIsDiscarded(t *testing.T) {
	q := NewQueue(time.Hour, 100)
	second := mustConversation(t, "u1", "["+turnTwo+"]")
	q.Push(second)
	q.Push(mustConversation(t, "u1", "["+turnOne+"]"))

	if q.Len() != 1 || q.Pop() != second {
		t.Fatal("an older turn must not displace its queued descendant")
	}
}

func TestQueue_ExpiresAfterTTL(t *testing.T) {
	q := NewQueue(time.Hour, 100)
	now := time.Now()
	q.now = func() time.Time { return now }
	q.Push(mustConversation(t, "u1", "["+turnOne+"]"))

	now = now.Add(time.Hour)
	if q.Pop() != nil {
		t.Fatal("expired conversation should be dropped")
	}
	if q.Len() != 0 {
		t.Fatal("expired conversation should be removed")
	}
}

func TestQueue_EvictsOldestWhenFull(t *testing.T) {
	q := NewQueue(time.Hour, 2)
	a := mustConversation(t, "u1", "["+turnOne+"]")
	b := mustConversation(t, "u2", "["+turnOne+"]")
	c := mustConversation(t, "u3", "["+turnOne+"]")

	q.Push(a)
	q.Push(b)
	q.Push(c)

	if q.Len() != 2 || q.Pop() != b || q.Pop() != c {
		t.Fatal("oldest entry should be evicted when the queue is full")
	}
	q.Push(a)
	if q.Len() != 1 {
		t.Fatal("evicted hash must be forgotten so it can be re-queued")
	}
}
