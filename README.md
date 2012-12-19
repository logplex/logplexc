logplexc
========

Golang Wrapper for Logplex

This library handles some of the details in interactions with
[Logplex](https://github.com/heroku/logplex) for the purpose of
emitting logs.

One might use it something like this:

```go
	package main

	import (
	       "fmt"
	       "logplexc"
	       "time"
	)

	// Set up Logplex Client
	cfg := logplexc.Config{
		Logplex:    *logplexUrl,
		Token:      *logplexToken,
		HttpClient: *http.DefaultClient,
		Transport: http.Transport{},
	}

	cl := logplexc.NewClient(&cfg)
	procId := "Pid, or whatever"
	stats := client.BufferMessage(time.Now(), procId, []byte(messageBytes))
	// (Buffer more messages as one sees fit)

	// .BufferMessage and .Statistics returns statistics for
	// monitoring or deciding when to begin a Post.
	fmt.Printf("Framed: %d\n", stats.NumberFramed)

	resp, err := cl.PostMessages()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	// Must close resp.Body
	resp.Body.Close()
```

PostMessages() is synchronous and slow.  It is also intended to be
safe under concurrent access (see bSwapLock) so that with only brief
contention one can also call BufferMessage in parallel.
