// A client implementation that includes concurrency and dropping.
package logplexc

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type Stats struct {
	// Number of concurrent requests at the time of retrieval.
	Concurrency int32

	// Message-level statistics

	// Total messages submitted
	Total uint64

	// Incremented when a message is ignored outright because of
	// too much work being done already.
	Dropped uint64

	// Incremented when a log post request is not known to have
	// succeeded and one has given up waiting.
	Cancelled uint64

	// Incremented when a log post request is responded to,
	// affirming that the messages have been rejected.
	Rejected uint64

	// Incremented only when a positive response is received from
	// logplex.
	Successful uint64

	// Request-level statistics

	TotalRequests   uint64
	CancelRequests  uint64
	RejectRequests  uint64
	SuccessRequests uint64
}

type Client struct {
	Stats
	statLock sync.Mutex

	c *MiniClient

	// Concurrency control of POST workers: the current level of
	// concurrency, and a token bucket channel.
	concurrency int32
	bucket      chan bool

	// Threshold of logplex request size to trigger POST.
	RequestSizeTrigger int

	// For forcing periodic posting of low-activity logs.
	ticker *time.Ticker

	// Closed when cleaning up
	finalize chan bool
}

type Config struct {
	MiniConfig
	RequestSizeTrigger int
	Concurrency        int
	TargetLogLatency   time.Duration
}

func NewClient(cfg *Config) (*Client, error) {
	c, err := NewMiniClient(&cfg.MiniConfig)
	if err != nil {
		return nil, err
	}

	m := Client{
		c:                  c,
		finalize:           make(chan bool),
		bucket:             make(chan bool),
		RequestSizeTrigger: cfg.RequestSizeTrigger,
		ticker:             time.NewTicker(cfg.TargetLogLatency),
	}

	// Supply tokens to the buckets.
	//
	// This goroutine exits when it has supplied all of the
	// initial tokens: that's because worker goroutines are
	// responsible for re-inserting tokens.
	go func() {
		for i := 0; i < cfg.Concurrency; i += 1 {
			m.bucket <- true
		}
	}()

	// Periodic log-sending ticker for responsive low-volume
	// logging.
	go func() {
		for {
			// Wait for a while to do work, or to exit
			select {
			case <-m.ticker.C:
			case _, _ = <-m.finalize:
				return
			}

			// Avoid sending empty requests
			s := m.c.Statistics()
			if s.NumberFramed > 0 {
				go m.syncWorker()
			}
		}
	}()

	return &m, nil
}

func (m *Client) Close() {
	// Clean up otherwise immortal ticker goroutine
	m.ticker.Stop()
	close(m.finalize)
}

func (m *Client) BufferMessage(
	when time.Time, procId string, log []byte) error {

	select {
	case _, _ = <-m.finalize:
		return errors.New("Failed trying to buffer a message: " +
			"client already Closed")
	default:
		// no-op
	}

	m.statLock.Lock()
	defer m.statLock.Unlock()

	s := m.c.BufferMessage(when, procId, log)
	if s.Buffered >= m.RequestSizeTrigger {
		go m.syncWorker()
	}

	return nil
}

func (m *Client) Statistics() (s Stats) {
	m.statLock.Lock()
	defer m.statLock.Unlock()

	s = m.Stats
	return s
}

func (m *Client) syncWorker() {
	atomic.AddInt32(&m.Stats.Concurrency, 1)
	defer atomic.AddInt32(&m.Stats.Concurrency, -1)

	// Check if there are any worker tokens available. If not,
	// then signal someone else to do the work and exit.
	select {
	case <-m.bucket:
		// When exiting, free up the token for use by another
		// worker.
		defer func() {
			m.bucket <- true
		}()
	default:
		return
	}

	// Post to logplex.
	resp, s, err := m.c.PostMessages()
	if err != nil {
		m.statReqErr(&s)
	}

	defer resp.Body.Close()

	// Check HTTP return code and accrue statistics accordingly.
	if resp.StatusCode != http.StatusNoContent {
		m.statReqRej(&s)
	} else {
		m.statReqSuccess(&s)
	}

	return
}

func (m *Client) statReqTotalUnsync(s *MiniStats) {
	m.Total += s.NumberFramed
	m.TotalRequests += 1
}

func (m *Client) statReqSuccess(s *MiniStats) {
	m.statLock.Lock()
	defer m.statLock.Unlock()
	m.statReqTotalUnsync(s)

	m.Successful += s.NumberFramed
	m.SuccessRequests += 1
}

func (m *Client) statReqErr(s *MiniStats) {
	m.statLock.Lock()
	defer m.statLock.Unlock()
	m.statReqTotalUnsync(s)

	m.Cancelled += s.NumberFramed
	m.CancelRequests += 1
}

func (m *Client) statReqRej(s *MiniStats) {
	m.statLock.Lock()
	defer m.statLock.Unlock()
	m.statReqTotalUnsync(s)

	m.Rejected += s.NumberFramed
	m.RejectRequests += 1
}
