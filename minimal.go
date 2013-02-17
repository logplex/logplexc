package logplexc

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// Running statistics on Client operation
type MiniStats struct {
	NumberFramed uint64
	Buffered     int
}

// Configuration of a Client.
//
// The configuration is by-value to prevent most kinds of accidental
// sharing of modifications between clients.  Also, modification of
// the URL is compulsary in the constructor, and it's not desirable to
// modify the version passed by the user as a side effect of
// constructing a client instance.
type MiniConfig struct {
	Logplex    url.URL
	Token      string
	HttpClient http.Client
}

// A bundle of messages that are either being accrued to or in the
// progress of being sent.
//
// This is packaged into its own type as it is handy to be able to
// manipulate bundles to pipeline buffering new logs to be written
// with bundles that have I/O in progress.
type Bundle struct {
	MiniStats
	outbox bytes.Buffer
}

// Client context: generally, at a minimum, one should exist per
// Logplex credential serviced by the program.
type MiniClient struct {
	// Configuration that should not be mutated after creation
	MiniConfig

	reqInFlight sync.WaitGroup

	// Messages that have been collected but not yet sent.
	bSwapLock sync.Mutex
	b         *Bundle
}

func NewMiniClient(cfg *MiniConfig) (client *MiniClient, err error) {
	c := MiniClient{}

	c.b = &Bundle{outbox: bytes.Buffer{}}

	// Make a private copy
	c.MiniConfig = *cfg

	// If the username and password weren't part of the URL, use
	// the logplex-token as the password
	if c.Logplex.User == nil {
		c.Logplex.User = url.UserPassword("token", c.Token)
	}

	return &c, nil
}

// Unsynchronized statistics gathering function
//
// Useful as a subroutine for procedures that already have taken care
// of synchronization.
func unsyncStats(b *Bundle) MiniStats {
	return b.MiniStats
}

// Copy the statistics structure embedded in the client.
func (c *MiniClient) Statistics() MiniStats {
	c.bSwapLock.Lock()
	defer c.bSwapLock.Unlock()

	return unsyncStats(c.b)
}

// Buffer a message for best-effort delivery to Logplex
//
// Return the critical statistics on what has been buffered so far so
// that the caller can opt to PostMessages() and empty the buffer.
//
// No effort is expended to clean up bad bytes disallowed by syslog,
// as Logplex has a length-prefixed format and the intention that each
// Client will only process messages for a single user/security
// context, so at worst it seems a buggy or malicious emitter of logs
// can cause problems for themselves only.
func (c *MiniClient) BufferMessage(
	when time.Time, host string, procId string, log []byte) MiniStats {
	// Avoid racing against other operations that may want to swap
	// out client's current bundle.
	c.bSwapLock.Lock()
	defer c.bSwapLock.Unlock()

	ts := when.UTC().Format(time.RFC3339)
	syslogPrefix := "<134>1 " + ts + " " + host + " " +
		c.Token + " " + procId + " - - "
	msgLen := len(syslogPrefix) + len(log)

	fmt.Fprintf(&c.b.outbox, "%d %s%s", msgLen, syslogPrefix, log)
	c.b.NumberFramed += 1
	c.b.Buffered = c.b.outbox.Len()

	return unsyncStats(c.b)
}

func (c *MiniClient) SwapBundle() Bundle {
	// Swap out the bundle for a fresh one, so that buffering can
	// continue again immediately.  It's the caller's perogative
	// to submit the Bundle to logplex.
	c.bSwapLock.Lock()
	defer c.bSwapLock.Unlock()

	var newB Bundle
	var oldB Bundle

	oldB = *c.b
	c.b = &newB

	return oldB
}

func (c *MiniClient) Post(b *Bundle) (*http.Response, error) {
	// Record that a request is in progress so that a clean
	// shutdown can wait for it to complete.
	c.reqInFlight.Add(1)
	defer c.reqInFlight.Done()

	req, err := http.NewRequest("POST", c.Logplex.String(), &b.outbox)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Content-Type", "application/logplex-1")
	req.Header.Add("Logplex-Msg-Count",
		strconv.FormatUint(b.NumberFramed, 10))

	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}
