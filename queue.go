package main

import (
	"container/list"
	"sync"
	"time"
)

type queueEntry struct {
	conv    *Conversation
	expires time.Time
}

// Queue is a FIFO of conversations awaiting classification. Every prefix hash
// of a queued conversation is indexed, so a newer turn replaces the queued
// version of the same chat and an older turn arriving late is discarded.
// Entries are dropped after ttl or when the queue is full.
type Queue struct {
	mu       sync.Mutex
	items    *list.List
	byPrefix map[string]*list.Element
	ttl      time.Duration
	maxSize  int
	now      func() time.Time
}

func NewQueue(ttl time.Duration, maxSize int) *Queue {
	return &Queue{
		items:    list.New(),
		byPrefix: make(map[string]*list.Element),
		ttl:      ttl,
		maxSize:  maxSize,
		now:      time.Now,
	}
}

func (q *Queue) Push(conv *Conversation) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if el, ok := q.byPrefix[conv.Hash()]; ok && len(el.Value.(*queueEntry).conv.Turns) > len(conv.Turns) {
		return
	}
	for _, prefix := range conv.Prefixes {
		if el, ok := q.byPrefix[prefix]; ok {
			q.remove(el)
		}
	}
	for q.items.Len() >= q.maxSize {
		q.remove(q.items.Front())
	}
	el := q.items.PushBack(&queueEntry{conv: conv, expires: q.now().Add(q.ttl)})
	for _, prefix := range conv.Prefixes {
		q.byPrefix[prefix] = el
	}
}

// Pop returns the oldest unexpired conversation, or nil if the queue is empty.
func (q *Queue) Pop() *Conversation {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := q.now()
	for el := q.items.Front(); el != nil; el = q.items.Front() {
		entry := el.Value.(*queueEntry)
		q.remove(el)
		if now.Before(entry.expires) {
			return entry.conv
		}
	}
	return nil
}

func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.items.Len()
}

func (q *Queue) remove(el *list.Element) {
	for _, prefix := range el.Value.(*queueEntry).conv.Prefixes {
		if q.byPrefix[prefix] == el {
			delete(q.byPrefix, prefix)
		}
	}
	q.items.Remove(el)
}
