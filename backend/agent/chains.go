package agent

import (
	"sync"
	"time"

	"tonelab/backend/daw"
)

// ChainStore keeps enumerated chains between runs. Where they live is the
// app's business; when they are trusted is decided here.
type ChainStore interface {
	Load() (map[int][]daw.FX, error)
	Save(map[int][]daw.FX) error
}

// ChainCache serves the last known chain at once and walks the DAW behind
// it. A walk of one 220-parameter instrument measures 2.8 s on REAPER, and
// with only the ten second memory every pause in a conversation paid it
// again before the first find_params could answer. Shared by the live and
// preview tools, since both look at the same project.
type ChainCache struct {
	mu      sync.Mutex
	store   ChainStore
	chains  map[int]cachedChain
	walking map[int]chan struct{}
	failed  map[int]error
}

type cachedChain struct {
	chain []daw.FX
	// at is zero for a chain read from the store: known, not yet seen this
	// run, so served with a note and never written through.
	at time.Time
}

func NewChainCache(store ChainStore) *ChainCache {
	c := &ChainCache{store: store, chains: map[int]cachedChain{}, walking: map[int]chan struct{}{}, failed: map[int]error{}}
	if store != nil {
		if known, err := store.Load(); err == nil {
			for track, chain := range known {
				c.chains[track] = cachedChain{chain: chain}
			}
		}
	}
	return c
}

// get returns the chain for a track. With fresh set it waits for a walk
// made this run and under chainTTL old, which is what a write needs: a
// parameter index from a previous session may now belong to another
// plugin. Without it a known chain is returned at once, stale flagged, and
// a walk started behind it so the next call, usually the write, finds it done.
func (c *ChainCache) get(source fxer, track int, fresh bool) (chain []daw.FX, stale bool, err error) {
	c.mu.Lock()
	entry, known := c.chains[track]
	if known && !entry.at.IsZero() && time.Since(entry.at) < chainTTL {
		c.mu.Unlock()
		return entry.chain, false, nil
	}
	done, walking := c.walking[track]
	if !walking {
		done = make(chan struct{})
		c.walking[track] = done
		go c.walk(source, track, done)
	}
	if known && !fresh {
		c.mu.Unlock()
		return entry.chain, true, nil
	}
	c.mu.Unlock()

	<-done
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.failed[track]; err != nil {
		return nil, false, err
	}
	return c.chains[track].chain, false, nil
}

func (c *ChainCache) walk(source fxer, track int, done chan struct{}) {
	chain, err := source.FXChain(track, chainTimeout)
	c.mu.Lock()
	defer c.mu.Unlock()
	defer close(done)
	delete(c.walking, track)
	if err != nil {
		c.failed[track] = err
		return
	}
	delete(c.failed, track)
	c.chains[track] = cachedChain{chain: chain, at: time.Now()}
	if c.store != nil {
		known := make(map[int][]daw.FX, len(c.chains))
		for number, entry := range c.chains {
			known[number] = entry.chain
		}
		// Written after every walk rather than at exit, since a desktop app
		// is closed by whatever means the user has to hand.
		_ = c.store.Save(known)
	}
}
