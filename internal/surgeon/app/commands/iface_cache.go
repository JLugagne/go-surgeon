package commands

import "sync"

// ifaceLRU is a small in-memory LRU cache for resolved interfaces.
// It avoids repeated calls to packages.Load (which invokes the Go toolchain
// and takes several seconds) when the same interface is resolved multiple
// times within a single MCP server session.
//
// Capacity is intentionally small (default 50) — each entry holds a
// *types.Interface which is lightweight (method signatures only, no AST).
type ifaceLRU struct {
  mu    sync.Mutex
  cap   int
  keys  []string
  items map[string]resolvedIface
}

func newIfaceLRU(cap int) *ifaceLRU {
  return &ifaceLRU{
    cap:   cap,
    keys:  make([]string, 0, cap),
    items: make(map[string]resolvedIface, cap),
  }
}

// get returns the cached value and true if found, promoting it to most-recent.
func (c *ifaceLRU) get(key string) (resolvedIface, bool) {
  c.mu.Lock()
  defer c.mu.Unlock()
  v, ok := c.items[key]
  if !ok {
    return resolvedIface{}, false
  }
  c.promote(key)
  return v, true
}

// set stores a value, evicting the least-recently-used entry if at capacity.
func (c *ifaceLRU) set(key string, v resolvedIface) {
  c.mu.Lock()
  defer c.mu.Unlock()
  if _, ok := c.items[key]; ok {
    c.promote(key)
    c.items[key] = v
    return
  }
  if len(c.keys) >= c.cap {
    evict := c.keys[0]
    c.keys = c.keys[1:]
    delete(c.items, evict)
  }
  c.keys = append(c.keys, key)
  c.items[key] = v
}

// promote moves key to the end of the keys slice (most-recently-used position).
// Must be called with c.mu held.
func (c *ifaceLRU) promote(key string) {
  for i, k := range c.keys {
    if k == key {
      c.keys = append(c.keys[:i], c.keys[i+1:]...)
      c.keys = append(c.keys, key)
      return
    }
  }
}
