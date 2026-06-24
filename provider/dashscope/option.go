package dashscope

import "net/http"

// Option configures a DashScope provider.
type Option func(*Provider)

// WithHTTPClient sets a custom HTTP client (e.g. for timeouts, proxies, or testing).
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.client = c }
}

// WithName overrides the provider name returned by Name() (default "dashscope").
// Use this to distinguish DashScope regions in logs and observability, for example:
//
//	dashscope.New(key, model, "https://dashscope-intl.aliyuncs.com/api/v1",
//	    dashscope.WithName("dashscope-intl"))
func WithName(name string) Option {
	return func(p *Provider) { p.name = name }
}

// WithDownloadVideo makes video models return the generated video as inline
// bytes (Attachment.Data) instead of a URL reference. By default video results
// are returned as a URL only, since DashScope video URLs are large and valid
// for a limited time — callers who need to persist the bytes opt in here.
// Downloaded media is capped at 512 MiB; larger responses are rejected with an error.
func WithDownloadVideo() Option {
	return func(p *Provider) { p.downloadVideo = true }
}
