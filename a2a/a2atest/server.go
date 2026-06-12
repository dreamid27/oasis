package a2atest

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Serve starts an httptest server around any http.Handler and registers
// cleanup on t. With a2a.NewServer it is the one-liner for integration
// tests:
//
//	remote, err := a2a.Dial(ctx, a2atest.Serve(t, a2a.NewServer(ag)).URL)
//
// a2atest deliberately does not import a2a (the a2a package's own tests
// import a2atest; an a2a import here would be an import cycle), which is
// why Serve takes the handler rather than constructing the server itself.
func Serve(t testing.TB, h http.Handler) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}
