// SPDX-License-Identifier: GPL-3.0-only

package audio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// DefaultCacheMaxBytes is the size cap of the on-disk voice cache (100 MB).
const DefaultCacheMaxBytes int64 = 100 << 20

var cacheKeyPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// CacheKey identifies a voice note by what produced it: the speech text, voice,
// model, and speaking style. Fields are length-prefixed so they cannot blur together.
func CacheKey(text, voice, model, style string) string {
	hash := sha256.New()
	for _, field := range []string{text, voice, model, style} {
		fmt.Fprintf(hash, "%d:%s;", len(field), field)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// Cache stores finished voice notes on disk so a repeated message skips the
// speech service. When the total size passes the cap, the oldest entries go first.
type Cache struct {
	dir      string
	maxBytes int64
	mu       sync.Mutex
}

// NewCache creates a cache in dir (created on first write) capped at maxBytes.
func NewCache(dir string, maxBytes int64) *Cache {
	return &Cache{dir: dir, maxBytes: maxBytes}
}

type cacheMeta struct {
	DurationSec int    `json:"duration_sec"`
	Waveform    []byte `json:"waveform"`
}

func (c *Cache) paths(key string) (ogg, meta string) {
	return filepath.Join(c.dir, key+".ogg"), filepath.Join(c.dir, key+".json")
}

// Get returns the cached clip for key. A damaged entry is removed and reported as a miss.
func (c *Cache) Get(key string) (*Clip, bool) {
	if !cacheKeyPattern.MatchString(key) {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	oggPath, metaPath := c.paths(key)
	ogg, oggErr := os.ReadFile(oggPath)
	metaBytes, metaErr := os.ReadFile(metaPath)
	if oggErr != nil || metaErr != nil {
		if oggErr == nil || metaErr == nil {
			c.remove(key)
		}
		return nil, false
	}

	var meta cacheMeta
	if len(ogg) == 0 || json.Unmarshal(metaBytes, &meta) != nil || meta.DurationSec < 1 || len(meta.Waveform) != WaveformSize {
		c.remove(key)
		return nil, false
	}
	return &Clip{Ogg: ogg, DurationSec: meta.DurationSec, Waveform: meta.Waveform}, true
}

// Put stores clip under key and then trims the cache to its size cap.
func (c *Cache) Put(key string, clip *Clip) error {
	if !cacheKeyPattern.MatchString(key) {
		return fmt.Errorf("invalid cache key")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return fmt.Errorf("failed to create the voice cache folder: %w", err)
	}
	oggPath, metaPath := c.paths(key)
	metaBytes, err := json.Marshal(cacheMeta{DurationSec: clip.DurationSec, Waveform: clip.Waveform})
	if err != nil {
		return fmt.Errorf("failed to encode cache metadata: %w", err)
	}
	if err := writeFileAtomic(oggPath, clip.Ogg); err != nil {
		return err
	}
	if err := writeFileAtomic(metaPath, metaBytes); err != nil {
		_ = os.Remove(oggPath)
		return err
	}
	return c.trim()
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("failed to write a cache file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to finish a cache file: %w", err)
	}
	return nil
}

func (c *Cache) remove(key string) {
	oggPath, metaPath := c.paths(key)
	_ = os.Remove(oggPath)
	_ = os.Remove(metaPath)
}

// trim deletes the oldest entries until the cache fits its cap. Callers hold c.mu.
func (c *Cache) trim() error {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return fmt.Errorf("failed to read the voice cache folder: %w", err)
	}

	type item struct {
		key  string
		size int64
		time int64
	}
	byKey := map[string]*item{}
	var total int64
	for _, entry := range entries {
		name := entry.Name()
		key := strings.TrimSuffix(strings.TrimSuffix(name, ".ogg"), ".json")
		if key == name || !cacheKeyPattern.MatchString(key) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		it := byKey[key]
		if it == nil {
			it = &item{key: key}
			byKey[key] = it
		}
		it.size += info.Size()
		if strings.HasSuffix(name, ".ogg") {
			it.time = info.ModTime().UnixNano()
		}
		total += info.Size()
	}

	items := make([]*item, 0, len(byKey))
	for _, it := range byKey {
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].time != items[j].time {
			return items[i].time < items[j].time
		}
		return items[i].key < items[j].key
	})
	for _, it := range items {
		if total <= c.maxBytes {
			break
		}
		c.remove(it.key)
		total -= it.size
	}
	return nil
}
