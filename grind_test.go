package logplexc

import (
	"bytes"
	"crypto/tls"
	"log"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

const KB = 1024

type ClosableBuffer struct {
	bytes.Buffer
}

func (cb *ClosableBuffer) Close() error {
	return nil
}

type NoopTripper struct{}

func (n *NoopTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp := http.Response{
		StatusCode: http.StatusNoContent,
		Body:       &ClosableBuffer{},
	}

	return &resp, nil
}

var BogusLogplexUrl url.URL

func init() {
	url, err := url.Parse("https://locahost:23456")
	if err != nil {
		log.Fatalf("Could not parse url: %v", err)
	}

	BogusLogplexUrl = *url
}

// Try creating and tearing down lots of clients
func BenchmarkStartup(b *testing.B) {
	client := *http.DefaultClient
	client.Transport = &NoopTripper{}

	cfg := Config{
		Logplex:            BogusLogplexUrl,
		HttpClient:         client,
		RequestSizeTrigger: 100,
		Concurrency:        3,
		Period:             3 * time.Second,
		Token:              "a-token",
	}

	for i := 0; i < b.N; i += 1 {
		c, err := NewClient(&cfg)
		if err != nil {
			b.Fatalf("Could not create Client: %v", err)
		}

		c.Close()
	}
}

// Measure how costly non-transport machinery by writing out logs from
// one goroutine as fast as possible to a no-op transport, but
// accumulating statistics.
func doFanInOutBench(b *testing.B, c *Client, inputConcur int) {
	b.StopTimer()

	log := []byte(`It was the best of times, it was the worst of
times, it was the age of wisdom, it was the age of foolishness, it was
the epoch of belief, it was the epoch of incredulity, it was the
season of Light, it was the season of Darkness, it was the spring of
hope, it was the winter of despair, we had everything before us, we
had nothing before us, we were all going direct to heaven, we were all
going direct the other way - in short, the period was so far like the
present period, that some of its noisiest authorities insisted on its
being received, for good or for evil, in the superlative degree of
comparison only.`)

	defer c.Close()
	t := time.Now()

	done := make(chan bool, inputConcur)
	perGoroutinePayload := b.N / inputConcur

	b.StartTimer()

	// Split up the work and do it in some number of goroutines
	for i := 0; i < inputConcur; i += 1 {
		go func() {
			for i := 0; i < perGoroutinePayload; i += 1 {
				c.BufferMessage(t, "UK", "CharlesDickens", log)
			}

			done <- true
		}()
	}

	// Wait for the work to report as finished; otherwise the
	// benchmark would end too early.
	for i := 0; i < inputConcur; i += 1 {
		<-done
	}
}

func NewNoopClient(f interface {
	Fatalf(string, ...interface{})
},
	sizeTrigger int) *Client {
	client := *http.DefaultClient
	client.Transport = &NoopTripper{}

	cfg := Config{
		Logplex:            BogusLogplexUrl,
		HttpClient:         client,
		RequestSizeTrigger: sizeTrigger,
		Concurrency:        3,
		Period:             3 * time.Second,
		Token:              "a-token",
	}

	c, err := NewClient(&cfg)
	if err != nil {
		log.Fatalf("Could not construct new client: %v", err)
	}

	return c
}

func BenchmarkFanoutNoBuf(b *testing.B) {
	doFanInOutBench(b, NewNoopClient(b, 0), 1)
}

func BenchmarkFanout(b *testing.B) {
	doFanInOutBench(b, NewNoopClient(b, 100*KB), 1)
}

func BenchmarkFanInOutNoBuf(b *testing.B) {
	doFanInOutBench(b, NewNoopClient(b, 0), 500)
}

func BenchmarkFanInOut(b *testing.B) {
	doFanInOutBench(b, NewNoopClient(b, 100*KB), 500)
}

// Try logging to a real, live endpoint URL and token, specified by
// LOGPLEX_URL and LOGPLEX_TOKEN.
//
// This is deceptively fast because dropping will be very common, even
// on localhost.
func BenchmarkToUrl(b *testing.B) {
	b.StopTimer()

	if os.Getenv("LOGPLEX_URL") == "" ||
		os.Getenv("LOGPLEX_TOKEN") == "" {
		b.Fatal("Skipping, no LOGPLEX_URL and LOGPLEX_TOKEN " +
			"environment variable set")
		return
	}

	logplexUrl, err := url.Parse(os.Getenv("LOGPLEX_URL"))
	if err != nil {
		b.Fatalf("Could not parse logplex endpoint %q: %v",
			os.Getenv("LOGPLEX_URL"), err)
	}

	token := os.Getenv("LOGPLEX_TOKEN")
	if token == "" {
		b.Fatalf("Invalid LOGPLEX_TOKEN set: %q", token)
	}

	if err != nil {
		b.Fatalf("Could not parse logplex endpoint %q: %v",
			os.Getenv("LOGPLEX_URL"), err)
	}

	client := *http.DefaultClient
	client.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	cfg := Config{
		Logplex:            *logplexUrl,
		HttpClient:         client,
		RequestSizeTrigger: 100 * KB,
		Concurrency:        3,
		Period:             3 * time.Second,
		Token:              token,
	}

	c, err := NewClient(&cfg)
	if err != nil {
		b.Fatalf("Could not create Client: %v", err)
	}

	doFanInOutBench(b, c, 1)
}
