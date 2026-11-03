// Package provider supplies bounded JSON transport; no live payment adapter is enabled.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

type Client struct{ http *http.Client }

func New(timeout time.Duration) (*Client, error) {
	if timeout < time.Millisecond || timeout > time.Minute {
		return nil, errors.New("invalid provider timeout")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxConnsPerHost = 8
	transport.MaxIdleConns = 16
	transport.MaxIdleConnsPerHost = 8
	transport.ResponseHeaderTimeout = timeout
	return &Client{http: &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

// DoJSON never returns a raw provider error/body, which can contain credentials or PII.
func (c *Client) DoJSON(ctx context.Context, request *http.Request, result any) error {
	response, err := c.http.Do(request.Clone(ctx))
	if err != nil {
		return errors.New("provider request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("provider rejected request")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return errors.New("invalid provider response size")
	}
	if json.Unmarshal(data, result) != nil {
		return errors.New("invalid provider JSON")
	}
	return nil
}
