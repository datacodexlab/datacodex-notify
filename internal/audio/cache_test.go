// SPDX-License-Identifier: GPL-3.0-only

package audio

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sampleClip(size int, duration int) *Clip {
	waveform := make([]byte, WaveformSize)
	waveform[3] = 42
	return &Clip{Ogg: make([]byte, size), DurationSec: duration, Waveform: waveform}
}

func TestCacheKeyDependsOnEveryField(t *testing.T) {
	base := CacheKey("text", "Kore", "model", "style")
	if len(base) != 64 || base != CacheKey("text", "Kore", "model", "style") {
		t.Fatal("key must be a stable 64-character hash")
	}
	for name, other := range map[string]string{
		"text":  CacheKey("text2", "Kore", "model", "style"),
		"voice": CacheKey("text", "Charon", "model", "style"),
		"model": CacheKey("text", "Kore", "model2", "style"),
		"style": CacheKey("text", "Kore", "model", "style2"),
		"blur":  CacheKey("tex", "tKore", "model", "style"),
	} {
		if other == base {
			t.Errorf("changing the %s must change the key", name)
		}
	}
}

func TestCacheHitAndMiss(t *testing.T) {
	cache := NewCache(filepath.Join(t.TempDir(), "voice-cache"), DefaultCacheMaxBytes)
	key := CacheKey("hello", "Kore", "m", "s")

	if _, ok := cache.Get(key); ok {
		t.Fatal("an empty cache must miss")
	}

	clip := sampleClip(1000, 4)
	clip.Ogg[0] = 'O'
	if err := cache.Put(key, clip); err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	got, ok := cache.Get(key)
	if !ok || got.DurationSec != 4 || got.Ogg[0] != 'O' || len(got.Ogg) != 1000 || got.Waveform[3] != 42 {
		t.Fatalf("unexpected cache hit: %+v ok=%v", got, ok)
	}
	if _, ok := cache.Get(CacheKey("other", "Kore", "m", "s")); ok {
		t.Error("a different key must miss")
	}
}

func TestCacheRejectsUnsafeKeys(t *testing.T) {
	dir := t.TempDir()
	cache := NewCache(dir, DefaultCacheMaxBytes)
	for _, key := range []string{"", "../escape", "short", "ZZZZ" + CacheKey("a", "b", "c", "d")[4:]} {
		if err := cache.Put(key, sampleClip(10, 1)); err == nil {
			t.Errorf("Put accepted the key %q", key)
		}
		if _, ok := cache.Get(key); ok {
			t.Errorf("Get accepted the key %q", key)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("unsafe keys wrote files: %d entries", len(entries))
	}
}

func TestCacheEvictsOldestFirst(t *testing.T) {
	dir := t.TempDir()
	// Each entry is 1000 bytes of audio plus a small metadata file; the cap holds two.
	cache := NewCache(dir, 2600)

	keys := []string{CacheKey("a", "", "", ""), CacheKey("b", "", "", ""), CacheKey("c", "", "", "")}
	base := time.Now().Add(-time.Hour)
	for i, key := range keys {
		if err := cache.Put(key, sampleClip(1000, 2)); err != nil {
			t.Fatalf("Put failed: %v", err)
		}
		oggPath, _ := cache.paths(key)
		// Explicit ages keep the test independent of the file system clock resolution.
		age := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(oggPath, age, age); err != nil {
			t.Fatal(err)
		}
	}
	// The third Put already pushed the total over the cap and evicted the oldest.
	if _, ok := cache.Get(keys[0]); ok {
		t.Error("the oldest entry should have been evicted")
	}

	fourth := CacheKey("d", "", "", "")
	if err := cache.Put(fourth, sampleClip(1000, 2)); err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	if _, ok := cache.Get(keys[1]); ok {
		t.Error("the next oldest entry should have been evicted")
	}
	for _, key := range []string{keys[2], fourth} {
		if _, ok := cache.Get(key); !ok {
			t.Errorf("entry %s should have survived", key[:8])
		}
	}

	var total int64
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		info, _ := e.Info()
		total += info.Size()
	}
	if total > 2600 {
		t.Errorf("cache is %d bytes, over the cap", total)
	}
}

func TestCacheRemovesDamagedEntries(t *testing.T) {
	dir := t.TempDir()
	cache := NewCache(dir, DefaultCacheMaxBytes)
	key := CacheKey("x", "Kore", "m", "s")
	if err := cache.Put(key, sampleClip(500, 3)); err != nil {
		t.Fatal(err)
	}
	_, metaPath := cache.paths(key)

	if err := os.WriteFile(metaPath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Get(key); ok {
		t.Fatal("a damaged entry must miss")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the damaged entry was not removed: %d files", len(entries))
	}

	// An audio file without metadata is also dropped.
	oggPath, _ := cache.paths(key)
	if err := os.WriteFile(oggPath, []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Get(key); ok {
		t.Fatal("an orphan audio file must miss")
	}
	if _, err := os.Stat(oggPath); !os.IsNotExist(err) {
		t.Error("the orphan audio file was not removed")
	}
}
