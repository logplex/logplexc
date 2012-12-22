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
type Stats struct {
	NumberFramed uint64
	Buffered     int
}

// Configuration of a Client.
//
// The configuration is by-value to prevent accidental sharing of
// modifications between clients.
type Config struct {
	Logplex    url.URL
	Token      string
	HttpClient http.Client
	Transport  http.Transport
}

// A bundle of messages that are either being accrued to or in the
// progress of being sent.
//
// This is packaged into its own type as it is handy to be able to
// manipulate bundles to pipeline buffering new logs to be written
// with bundles that have I/O in progress.
type bundle struct {
	nFramed uint64
	outbox  bytes.Buffer
}

// Client context: generally, at a minimum, one should exist per
// Logplex credential serviced by the program.
type Client struct {
	Stats

	// Configuration that should not be mutated after creation
	Config

	reqInFlight sync.WaitGroup

	// Messages that have been collected but not yet sent.
	bSwapLock sync.Mutex
	b         *bundle
}

func NewClient(cfg *Config) (client *Client, err error) {
	c := Client{}

	c.b = &bundle{outbox: bytes.Buffer{}}

	// Make a private copy
	c.Config = *cfg

	// If the username and password weren't part of the URL, use
	// the logplex-token as the password
	if c.Logplex.User == nil {
		c.Logplex.User = url.UserPassword("token", c.Token)
	}

	// http.Client expects to have a transport reference; fix up
	// the pointer to point the copy of Transport passed.
	c.Config.HttpClient.Transport = &c.Transport

	return &c, nil
}

// Unsynchronized statistics gathering function
//
// Useful as a subroutine for procedures that already have taken care
// of synchronization.
func unsyncStats(b *bundle) Stats {
	return Stats{
		NumberFramed: b.nFramed,
		Buffered:     b.outbox.Len(),
	}
}

// Copy the statistics structure embedded in the client.
func (c *Client) Statistics() Stats {
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
func (c *Client) BufferMessage(when time.Time, procId string, log []byte) Stats {
	// Avoid racing against other operations that may want to swap
	// out client's current bundle.
	c.bSwapLock.Lock()
	defer c.bSwapLock.Unlock()

	ts := when.UTC().Format(time.RFC3339)
	syslogPrefix := "<134>1 " + ts + " 1234 " +
		c.Token + " " + procId + " - - "
	msgLen := len(syslogPrefix) + len(log)

	fmt.Fprintf(&c.b.outbox, "%d %s%s", msgLen, syslogPrefix, log)
	c.b.nFramed += 1

	return unsyncStats(c.b)
}

// Post messages that are pending being posted.
func (c *Client) PostMessages() (*http.Response, error) {
	var b bundle

	// Swap out the bundle that is about to go through a long I/O
	// operation for a fresh one, so that buffering can continue
	// again immediately.
	func() {
		c.bSwapLock.Lock()
		defer c.bSwapLock.Unlock()

		b = *c.b
		c.b.outbox = bytes.Buffer{}
		c.b.nFramed = 0
	}()

	// Record that a request is in progress so that a clean
	// shutdown can wait for it to complete.
	c.reqInFlight.Add(1)
	defer c.reqInFlight.Done()

	req, _ := http.NewRequest("POST", c.Logplex.String(), &b.outbox)
	req.Header.Add("Content-Type", "application/logplex-1")
	req.Header.Add("Logplex-Msg-Count", strconv.FormatUint(b.nFramed, 10))

	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}
