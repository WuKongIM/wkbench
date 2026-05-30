// Package benchapi contains a small black-box client for WuKongIM /bench/v1 APIs.
package benchapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultTimeout = 10 * time.Second

// Config controls the bench API client.
type Config struct {
	// APIAddrs are target HTTP API base addresses tried in order.
	APIAddrs []string
	// Token is the optional bearer token.
	Token string
	// HTTPClient overrides the default client for tests.
	HTTPClient *http.Client
}

// Client calls WuKongIM benchmark APIs without server internals.
type Client struct {
	cfg  Config
	http *http.Client
}

// NewClient creates a bench API client.
func NewClient(cfg Config) *Client {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{cfg: cfg, http: hc}
}

// Healthz checks /healthz.
func (c *Client) Healthz(ctx context.Context) error {
	return c.getAny(ctx, "/healthz", nil)
}

// Readyz checks /readyz.
func (c *Client) Readyz(ctx context.Context) error {
	return c.getAny(ctx, "/readyz", nil)
}

// Capabilities reads /bench/v1/capabilities.
func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var out Capabilities
	if err := c.getAny(ctx, "/bench/v1/capabilities", &out); err != nil {
		return Capabilities{}, err
	}
	return out, nil
}

// UpsertTokens posts batch user tokens.
func (c *Client) UpsertTokens(ctx context.Context, req BatchTokensRequest) error {
	return c.postAny(ctx, "/bench/v1/users/tokens", req)
}

// UpsertChannels posts batch channels.
func (c *Client) UpsertChannels(ctx context.Context, req BatchChannelsRequest) error {
	return c.postAny(ctx, "/bench/v1/channels", req)
}

// AddSubscribers posts batch subscribers.
func (c *Client) AddSubscribers(ctx context.Context, req BatchSubscribersRequest) error {
	return c.postAny(ctx, "/bench/v1/channels/subscribers", req)
}

func (c *Client) getAny(ctx context.Context, path string, out any) error {
	for _, addr := range c.addrs() {
		if err := c.doJSON(ctx, http.MethodGet, addr, path, nil, out); err == nil {
			return nil
		}
	}
	return fmt.Errorf("all target api addresses failed for GET %s", path)
}

func (c *Client) postAny(ctx context.Context, path string, body any) error {
	for _, addr := range c.addrs() {
		if err := c.doJSON(ctx, http.MethodPost, addr, path, body, nil); err == nil {
			return nil
		}
	}
	return fmt.Errorf("all target api addresses failed for POST %s", path)
}

func (c *Client) doJSON(ctx context.Context, method, base, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s returned status %d", method, req.URL.String(), resp.StatusCode)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) addrs() []string {
	out := make([]string, 0, len(c.cfg.APIAddrs))
	for _, addr := range c.cfg.APIAddrs {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			out = append(out, addr)
		}
	}
	return out
}

// Capabilities is the subset of /bench/v1/capabilities needed by these units.
type Capabilities struct {
	// Enabled confirms the benchmark API is available.
	Enabled bool `json:"enabled"`
	// Version is the target bench API version.
	Version string `json:"version"`
}

// BatchTokensRequest updates benchmark user tokens in one batch.
type BatchTokensRequest struct {
	// RunID identifies the benchmark run that owns this batch.
	RunID string `json:"run_id"`
	// BatchID identifies this idempotent preparation batch.
	BatchID string `json:"batch_id"`
	// Upsert asks the target to create or update records where supported.
	Upsert bool `json:"upsert,omitempty"`
	// Users is the spec-shaped user token list accepted by bench/v1.
	Users []UserTokenItem `json:"users,omitempty"`
}

// UserTokenItem carries one benchmark user token update.
type UserTokenItem struct {
	// UID is the benchmark user identifier.
	UID string `json:"uid"`
	// Token is the authentication token assigned to the benchmark user.
	Token string `json:"token"`
}

// BatchChannelsRequest upserts benchmark channels in one batch.
type BatchChannelsRequest struct {
	// RunID identifies the benchmark run that owns this batch.
	RunID string `json:"run_id"`
	// BatchID identifies this idempotent preparation batch.
	BatchID string `json:"batch_id"`
	// Upsert asks the target to create or update records where supported.
	Upsert bool `json:"upsert,omitempty"`
	// Channels is the spec-shaped channel list accepted by bench/v1.
	Channels []ChannelItem `json:"channels,omitempty"`
}

// ChannelItem carries one benchmark channel upsert.
type ChannelItem struct {
	// ChannelID is the benchmark channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the WuKong channel type; group is currently 2.
	ChannelType uint8 `json:"channel_type"`
	// Large marks a large-group channel.
	Large bool `json:"large,omitempty"`
}

// BatchSubscribersRequest appends benchmark subscribers in one batch.
type BatchSubscribersRequest struct {
	// RunID identifies the benchmark run that owns this batch.
	RunID string `json:"run_id"`
	// BatchID identifies this idempotent preparation batch.
	BatchID string `json:"batch_id"`
	// Items carries subscriber mutations grouped by channel.
	Items []SubscriberItem `json:"items"`
}

// SubscriberItem carries subscribers to append for one group channel.
type SubscriberItem struct {
	// ChannelID is the benchmark channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the WuKong channel type; group is currently 2.
	ChannelType uint8 `json:"channel_type"`
	// Subscribers are user IDs appended to the channel subscriber list.
	Subscribers []string `json:"subscribers"`
}
