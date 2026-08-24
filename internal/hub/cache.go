package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CacheTTL is how long a cached credential stays usable.
//
// The hub is the authority and the machine is a cache. A short life keeps the
// two close together: a credential that `jf auth` replaced reaches every machine
// within this time, without any action on those machines. It is long enough that
// a shell loop of `gog` calls does not open a connection for each call.
const CacheTTL = 5 * time.Minute

// cacheEntry is one credential on disk, with the time it was fetched.
type cacheEntry struct {
	Connection string `json:"connection"`
	Secret     string `json:"secret"`
	Identity   string `json:"identity"`
	UpdatedAt  int64  `json:"updated_at"`
	// FetchedAt is milliseconds since the epoch, on this machine's clock. The
	// TTL is measured against it rather than against the hub's UpdatedAt,
	// because UpdatedAt says when the credential changed, not when this machine
	// last confirmed it.
	FetchedAt int64 `json:"fetched_at"`
}

// Cache stores credentials from the hub in a directory.
//
// Every file has mode 0600, and the directory has mode 0700, because each file
// holds a plaintext secret.
type Cache struct {
	Directory string
	TTL       time.Duration
	// Now is the clock. Tests replace it. A nil value means time.Now.
	Now func() time.Time
}

// NewCache returns a cache in directory with the default lifetime.
func NewCache(directory string) *Cache {
	return &Cache{Directory: directory, TTL: CacheTTL}
}

func (cache *Cache) now() time.Time {
	if cache.Now != nil {
		return cache.Now()
	}
	return time.Now()
}

// path returns the file for one connection.
//
// The name is a hash, not the connection name. A connection name reaches the
// hub as a URL path segment and can hold a slash or a dot-dot, so using it as a
// file name would let the name choose where the file lands.
func (cache *Cache) path(connection string) string {
	sum := sha256.Sum256([]byte(connection))
	return filepath.Join(cache.Directory, hex.EncodeToString(sum[:])+".json")
}

// Get returns a cached credential that is still inside the lifetime.
//
// The second value is false when there is no usable entry, for every reason: no
// file, an unreadable file, an expired entry, or an entry for a different
// connection. A cache miss is never an error, because the caller then asks the
// hub, which is the authority.
func (cache *Cache) Get(connection string) (Credential, bool) {
	data, err := os.ReadFile(cache.path(connection))
	if err != nil {
		return Credential{}, false
	}
	var entry cacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return Credential{}, false
	}
	// The file name is a hash of the connection, so a mismatch means a hash
	// collision or a hand-edited file. Both are reasons to ask the hub.
	if entry.Connection != connection {
		return Credential{}, false
	}

	age := cache.now().Sub(time.UnixMilli(entry.FetchedAt))
	if age < 0 || age >= cache.TTL {
		return Credential{}, false
	}
	return Credential{
		Connection: entry.Connection,
		Secret:     entry.Secret,
		Identity:   entry.Identity,
		UpdatedAt:  entry.UpdatedAt,
	}, true
}

// Put writes one credential to the cache.
func (cache *Cache) Put(credential Credential) error {
	if err := os.MkdirAll(cache.Directory, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", cache.Directory, err)
	}
	encoded, err := json.Marshal(cacheEntry{
		Connection: credential.Connection,
		Secret:     credential.Secret,
		Identity:   credential.Identity,
		UpdatedAt:  credential.UpdatedAt,
		FetchedAt:  cache.now().UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("encode the cache entry: %w", err)
	}

	target := cache.path(credential.Connection)
	temporary := target + ".new"
	if err := os.WriteFile(temporary, encoded, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	if err := os.Rename(temporary, target); err != nil {
		os.Remove(temporary)
		return fmt.Errorf("write %s: %w", target, err)
	}
	return nil
}

// Forget removes one connection from the cache.
//
// `jf auth` calls this after a write, so the next read on this machine reaches
// the hub instead of returning the credential the write replaced.
func (cache *Cache) Forget(connection string) error {
	err := os.Remove(cache.path(connection))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove the cache entry for %q: %w", connection, err)
	}
	return nil
}
