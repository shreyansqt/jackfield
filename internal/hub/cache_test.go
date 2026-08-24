package hub

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestCache(t *testing.T, now func() time.Time) *Cache {
	t.Helper()
	cache := NewCache(filepath.Join(t.TempDir(), "creds"))
	cache.Now = now
	return cache
}

func TestCacheReturnsAFreshEntry(t *testing.T) {
	clock := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	cache := newTestCache(t, func() time.Time { return clock })

	credential := Credential{Connection: "slack-smarta", Secret: "xoxp-1", Identity: "shreyans@example.com"}
	if err := cache.Put(credential); err != nil {
		t.Fatal(err)
	}

	clock = clock.Add(CacheTTL - time.Second)
	cached, found := cache.Get("slack-smarta")
	if !found {
		t.Fatal("an entry inside the lifetime must be returned")
	}
	if cached.Secret != "xoxp-1" {
		t.Fatalf("got secret %q, want xoxp-1", cached.Secret)
	}
	if cached.Identity != "shreyans@example.com" {
		t.Fatalf("got identity %q, want shreyans@example.com", cached.Identity)
	}
}

func TestCacheDropsAnEntryAtTheLifetime(t *testing.T) {
	clock := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	cache := newTestCache(t, func() time.Time { return clock })

	if err := cache.Put(Credential{Connection: "slack-smarta", Secret: "xoxp-1"}); err != nil {
		t.Fatal(err)
	}

	clock = clock.Add(CacheTTL)
	if _, found := cache.Get("slack-smarta"); found {
		t.Fatal("an entry at the lifetime must expire; the hub is the authority")
	}
}

// A clock that moved backwards makes an entry look arbitrarily fresh. The cache
// treats a negative age as a miss and asks the hub instead.
func TestCacheDropsAnEntryFromTheFuture(t *testing.T) {
	clock := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	cache := newTestCache(t, func() time.Time { return clock })

	if err := cache.Put(Credential{Connection: "slack-smarta", Secret: "xoxp-1"}); err != nil {
		t.Fatal(err)
	}

	clock = clock.Add(-time.Hour)
	if _, found := cache.Get("slack-smarta"); found {
		t.Fatal("an entry newer than the clock must be a miss")
	}
}

func TestCacheMissesAnUnknownConnection(t *testing.T) {
	cache := newTestCache(t, time.Now)
	if _, found := cache.Get("never-stored"); found {
		t.Fatal("an unknown connection must be a miss")
	}
}

func TestCacheKeepsConnectionsApart(t *testing.T) {
	cache := newTestCache(t, time.Now)
	if err := cache.Put(Credential{Connection: "slack-smarta", Secret: "xoxp-work"}); err != nil {
		t.Fatal(err)
	}
	if err := cache.Put(Credential{Connection: "slack-personal", Secret: "xoxp-home"}); err != nil {
		t.Fatal(err)
	}

	work, _ := cache.Get("slack-smarta")
	home, _ := cache.Get("slack-personal")
	if work.Secret != "xoxp-work" || home.Secret != "xoxp-home" {
		t.Fatalf("got %q and %q, want xoxp-work and xoxp-home", work.Secret, home.Secret)
	}
}

func TestForgetRemovesAnEntry(t *testing.T) {
	cache := newTestCache(t, time.Now)
	if err := cache.Put(Credential{Connection: "slack-smarta", Secret: "xoxp-1"}); err != nil {
		t.Fatal(err)
	}
	if err := cache.Forget("slack-smarta"); err != nil {
		t.Fatal(err)
	}
	if _, found := cache.Get("slack-smarta"); found {
		t.Fatal("Forget must remove the entry, so the next read reaches the hub")
	}
	// Forgetting twice is not an error. `jf auth` calls it after every write.
	if err := cache.Forget("slack-smarta"); err != nil {
		t.Fatalf("forgetting an absent entry must succeed: %v", err)
	}
}

// Every cached file holds a plaintext secret, so no other user may read it.
func TestCacheFilesAreReadableOnlyByTheOwner(t *testing.T) {
	cache := newTestCache(t, time.Now)
	if err := cache.Put(Credential{Connection: "slack-smarta", Secret: "xoxp-1"}); err != nil {
		t.Fatal(err)
	}

	directory, err := os.Stat(cache.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if mode := directory.Mode().Perm(); mode != 0o700 {
		t.Fatalf("the cache directory has mode %04o, want 0700", mode)
	}

	entries, err := os.ReadDir(cache.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d files, want 1", len(entries))
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("the cache file has mode %04o, want 0600", mode)
	}
}

// A connection name reaches the hub as a URL path segment, so it may hold a
// slash or a dot-dot. The file name must not let the name choose a path.
func TestCacheContainsAConnectionNameWithPathCharacters(t *testing.T) {
	cache := newTestCache(t, time.Now)
	if err := cache.Put(Credential{Connection: "../../escape", Secret: "xoxp-1"}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(cache.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d files in the cache directory, want 1", len(entries))
	}
	cached, found := cache.Get("../../escape")
	if !found || cached.Secret != "xoxp-1" {
		t.Fatal("the entry must still be readable by its connection name")
	}
}
