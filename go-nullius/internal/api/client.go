// Package api wraps anthropic-sdk-go with go-nullius's auth policy:
// subscription OAuth (Bearer + anthropic-beta: oauth-2025-04-20) or
// x-api-key, chosen by the resolved credential; proactive refresh when
// the token is inside its expiry margin; one refresh-and-retry on 401.
package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"go-nullius/internal/auth"
)

// OAuthBeta is required on /v1/messages for subscription OAuth tokens.
const OAuthBeta = "oauth-2025-04-20"

type Client struct {
	resolver  *auth.Resolver
	extraOpts []option.RequestOption // preserved across rebuilds (tests: WithBaseURL)

	mu    sync.Mutex
	creds *auth.Credentials
	inner anthropic.Client
}

// New resolves credentials and builds the underlying SDK client.
// extraOpts (e.g. option.WithBaseURL in tests) apply to every rebuild.
func New(resolver *auth.Resolver, extraOpts ...option.RequestOption) (*Client, error) {
	creds, err := resolver.Load()
	if err != nil {
		return nil, err
	}
	c := &Client{resolver: resolver, extraOpts: extraOpts}
	c.adopt(creds)
	return c, nil
}

func (c *Client) adopt(creds *auth.Credentials) {
	opts := make([]option.RequestOption, 0, len(c.extraOpts)+2)
	if creds.OAuth() {
		opts = append(opts,
			option.WithAuthToken(creds.AccessToken),
			option.WithHeaderAdd("anthropic-beta", OAuthBeta),
		)
	} else {
		opts = append(opts, option.WithAPIKey(creds.AccessToken))
	}
	opts = append(opts, c.extraOpts...)
	c.creds = creds
	c.inner = anthropic.NewClient(opts...)
}

// ensureFresh refreshes proactively when the token is expired or inside
// its margin. No-op for API keys.
func (c *Client) ensureFresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.creds.OAuth() || !c.creds.Expired(time.Now()) {
		return nil
	}
	fresh, err := c.resolver.Refresh(ctx, c.creds)
	if fresh == nil {
		return fmt.Errorf("token expired and refresh failed: %w", err)
	}
	// A non-nil pair with a persist error is still usable; adopt it and
	// let the caller-visible error carry the persistence warning.
	c.adopt(fresh)
	return nil
}

// refreshAfter401 rotates the token once after an auth failure.
func (c *Client) refreshAfter401(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.creds.OAuth() {
		return errors.New("401 with API key auth: check ANTHROPIC_API_KEY")
	}
	fresh, err := c.resolver.Refresh(ctx, c.creds)
	if fresh == nil {
		return fmt.Errorf("refresh after 401 failed: %w", err)
	}
	c.adopt(fresh)
	return nil
}

// snapshot returns an addressable copy of the current SDK client (its
// service methods have pointer receivers).
func (c *Client) snapshot() *anthropic.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	inner := c.inner
	return &inner
}

func is401(err error) bool {
	var apierr *anthropic.Error
	return errors.As(err, &apierr) && apierr.StatusCode == 401
}

// Message issues a non-streaming call with the refresh policy applied.
func (c *Client) Message(ctx context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
	if err := c.ensureFresh(ctx); err != nil {
		return nil, err
	}
	msg, err := c.snapshot().Messages.New(ctx, params)
	if err != nil && is401(err) {
		if rerr := c.refreshAfter401(ctx); rerr != nil {
			return nil, rerr
		}
		msg, err = c.snapshot().Messages.New(ctx, params)
	}
	return msg, err
}

// Stream issues a streaming call, accumulating into a Message. onEvent,
// if non-nil, sees every raw stream event (for live text rendering).
// A 401 gets one refresh-and-retry, but only if no event was consumed
// yet — a stream that died mid-message must surface, not silently restart.
func (c *Client) Stream(ctx context.Context, params anthropic.MessageNewParams, onEvent func(anthropic.MessageStreamEventUnion)) (*anthropic.Message, error) {
	if err := c.ensureFresh(ctx); err != nil {
		return nil, err
	}
	msg, consumed, err := c.streamOnce(ctx, params, onEvent)
	if err != nil && !consumed && is401(err) {
		if rerr := c.refreshAfter401(ctx); rerr != nil {
			return nil, rerr
		}
		msg, _, err = c.streamOnce(ctx, params, onEvent)
	}
	return msg, err
}

func (c *Client) streamOnce(ctx context.Context, params anthropic.MessageNewParams, onEvent func(anthropic.MessageStreamEventUnion)) (*anthropic.Message, bool, error) {
	stream := c.snapshot().Messages.NewStreaming(ctx, params)
	msg := anthropic.Message{}
	consumed := false
	for stream.Next() {
		consumed = true
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return nil, consumed, fmt.Errorf("accumulate: %w", err)
		}
		if onEvent != nil {
			onEvent(ev)
		}
	}
	if err := stream.Err(); err != nil {
		return nil, consumed, err
	}
	return &msg, consumed, nil
}
