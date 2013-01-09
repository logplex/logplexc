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
	       "net/http"
	       "time"
	)

	// Set up an http client
	client := *http.DefaultClient
	client.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{},
	}

	// Set up Logplex Client
	cfg := logplexc.Config{
		Logplex:            *logplexUrl,
		HttpClient:         client,
		RequestSizeTrigger: 100 * KB,
		Concurrency:        3,
		Period:             3 * time.Second,
		Token:              "my-token",
	}

	cl, err := logplexc.NewClient(&cfg)
	procId := "Pid, or whatever"
	err := client.BufferMessage(time.Now(), procId, []byte(messageBytes))
	if err != nil {
		fmt.Printf("Couldn't buffer message: %v", err)
	}
```
