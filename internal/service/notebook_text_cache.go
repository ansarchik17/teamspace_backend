package service

import (
	"container/list"
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/west6ide/workspace-backend/internal/domain"
	"golang.org/x/sync/singleflight"
)

const (
	notebookTextCacheBytes = 256 * 1024 * 1024
	notebookTextCacheTTL   = 30 * time.Minute
	// Bounds an extraction shared by concurrent requests (download, parsing
	// and PDF OCR), independently of the request that started it.
	notebookTextLoadTimeout = 4 * time.Minute
)

// Extraction (download, parsing, PDF OCR) is the slowest part of every
// notebook request. The key covers every attribute that changes with the file
// content, so an edited file never reuses stale text. Errors are not cached.
type notebookTextCache struct {
	mu       sync.Mutex
	maxBytes int
	ttl      time.Duration
	bytes    int
	items    map[string]*list.Element
	order    *list.List
	group    singleflight.Group
}

type notebookTextCacheEntry struct {
	key     string
	text    string
	expires time.Time
}

func newNotebookTextCache(maxBytes int, ttl time.Duration) *notebookTextCache {
	return &notebookTextCache{maxBytes: maxBytes, ttl: ttl, items: map[string]*list.Element{}, order: list.New()}
}

func notebookTextCacheKey(f *domain.DriveFile) string {
	return fmt.Sprintf("%s:%s:%d:%d:%d", f.ID, f.ContentHash, f.Version, f.Size, f.UpdatedAt.UnixNano())
}

func (c *notebookTextCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return "", false
	}
	entry := el.Value.(*notebookTextCacheEntry)
	if time.Now().After(entry.expires) {
		c.remove(el)
		return "", false
	}
	c.order.MoveToFront(el)
	return entry.text, true
}

func (c *notebookTextCache) put(key, text string) {
	if len(text) > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.remove(el)
	}
	c.items[key] = c.order.PushFront(&notebookTextCacheEntry{key: key, text: text, expires: time.Now().Add(c.ttl)})
	c.bytes += len(text)
	for c.bytes > c.maxBytes {
		c.remove(c.order.Back())
	}
}

func (c *notebookTextCache) remove(el *list.Element) {
	entry := el.Value.(*notebookTextCacheEntry)
	c.order.Remove(el)
	delete(c.items, entry.key)
	c.bytes -= len(entry.text)
}

// load returns cached text or runs extract once for all concurrent callers.
// The shared extraction does not stop when one caller's ctx ends: it runs
// under its own deadline, and each caller stops waiting when its ctx ends.
// A nil cache simply extracts.
func (c *notebookTextCache) load(ctx context.Context, key string, extract func(context.Context) (string, error)) (string, error) {
	if c == nil {
		return extract(ctx)
	}
	if text, ok := c.get(key); ok {
		return text, nil
	}
	results := c.group.DoChan(key, func() (value interface{}, err error) {
		// DoChan turns a panic into a process crash; report it as an error.
		defer func() {
			if r := recover(); r != nil {
				value, err = "", fmt.Errorf("notebook text extraction panicked: %v\n%s", r, debug.Stack())
			}
		}()
		if text, ok := c.get(key); ok {
			return text, nil
		}
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notebookTextLoadTimeout)
		defer cancel()
		text, err := extract(loadCtx)
		if err != nil {
			return "", err
		}
		c.put(key, text)
		return text, nil
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-results:
		if result.Err != nil {
			return "", result.Err
		}
		return result.Val.(string), nil
	}
}
