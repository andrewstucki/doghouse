# Datadog Tracing Test Server

Sometimes you want to test the trace output you send to Datadog. This library facilitates that by acting as a test Datadog server that captures and stores any traces sent to it through its `httptest` server.

This library is fairly special-purpose as it hijacks the `DD_TRACE_AGENT_URL` environment variable in the running binary and ensures only one instance of the test server is ever running in a process. This is important due to the fact that the underlying Datadog tracer is global and multiple calls to reconfigure the tracer will ultimately affect any other running test - therefore, if this mock tracer is used, it should only ever be initialized at the beginning of a test suite run, and tests that use it should ensure that they don't conflict with each other (i.e. emitting the same traces). Alternatively, the tests should be run serially and the state of the server can be reset between runs via a call to `server.Reset()`.

## Example Usage

```go
func TestMain(m *testing.M) {
	server = doghouse.New()
	defer server.Close()
	os.Exit(m.Run())
}

func TestSpan(t *testing.T) {
	span := tracer.StartSpan("test.span")
	span.Finish()

	tracer.Flush()

	server.WaitForSpan(t, "test.span")
}
```

## Dependencies

This library uses `github.com/tinylib/msgp` for generating messagepack marshalers, you can install it with

```bash
go install github.com/tinylib/msgp
```

in order to run `go generate`.