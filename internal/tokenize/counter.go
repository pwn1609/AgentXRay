package tokenize

import (
	"fmt"
	"sync"

	"github.com/pkoukk/tiktoken-go"
	tiktokenloader "github.com/pkoukk/tiktoken-go-loader"
)

// DefaultEncoding is a reasonable OpenAI-family default. Counts for non-OpenAI
// providers (Anthropic, Gemini) are therefore approximate and are surfaced as
// "estimated" in the resulting TokenBreakdown.
const DefaultEncoding = "cl100k_base"

// offlineOnce installs the embedded BPE loader exactly once so the binary never
// reaches out to the network to fetch tokenizer vocab — this preserves the
// single-binary, zero-external-dependency property.
var offlineOnce sync.Once

func ensureOffline() {
	offlineOnce.Do(func() {
		tiktoken.SetBpeLoader(tiktokenloader.NewOfflineLoader())
	})
}

// Counter tokenizes text using a cached tiktoken encoding. It is safe for
// concurrent use.
type Counter struct {
	mu       sync.Mutex
	encs     map[string]*tiktoken.Tiktoken
	encoding string
}

// NewCounter returns a Counter using the named encoding (empty => DefaultEncoding).
// The encoding is loaded eagerly so configuration errors surface immediately.
func NewCounter(encoding string) (*Counter, error) {
	ensureOffline()
	if encoding == "" {
		encoding = DefaultEncoding
	}
	c := &Counter{encs: make(map[string]*tiktoken.Tiktoken), encoding: encoding}
	if _, err := c.enc(encoding); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Counter) enc(name string) (*tiktoken.Tiktoken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.encs[name]; ok {
		return e, nil
	}
	e, err := tiktoken.GetEncoding(name)
	if err != nil {
		return nil, fmt.Errorf("load tokenizer encoding %q: %w", name, err)
	}
	c.encs[name] = e
	return e, nil
}

// Count returns the number of tokens in text under the Counter's encoding.
func (c *Counter) Count(text string) int {
	if text == "" {
		return 0
	}
	e, err := c.enc(c.encoding)
	if err != nil {
		return 0 // encoding was validated in NewCounter; unreachable in practice
	}
	return len(e.Encode(text, nil, nil))
}
