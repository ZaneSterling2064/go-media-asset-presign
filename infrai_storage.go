package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const canonicalOperation = "infrai.storage.object.presign"

type infraiError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *infraiError) Error() string {
	return e.Code + ": " + e.Message
}

type envelope struct {
	OK       bool            `json:"ok"`
	Found    *bool           `json:"found,omitempty"`
	Data     json.RawMessage `json:"data"`
	Error    *apiError       `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

type storageClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	sleep      func(context.Context, time.Duration) error
}

func newStorageClient(baseURL, apiKey string, httpClient *http.Client) *storageClient {
	return &storageClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: httpClient,
		sleep: func(ctx context.Context, delay time.Duration) error {
			select {
			case <-time.After(delay):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}

func (c *storageClient) createBucket(ctx context.Context, name string) error {
	return c.call(ctx, http.MethodPost, "/v1/storage/bucket/create", map[string]string{"name": name}, nil)
}

type presignRequest struct {
	Op                  string `json:"op"`
	ExpiresSeconds      int    `json:"expires_seconds,omitempty"`
	ContentType         string `json:"content_type,omitempty"`
	MaxBytes            int64  `json:"max_bytes,omitempty"`
	ResponseDisposition string `json:"response_disposition,omitempty"`
	IdempotencyKey      string `json:"idempotency_key,omitempty"`
}

type presignResult struct {
	URL string `json:"url"`
}

func (c *storageClient) presign(ctx context.Context, bucket, key string, request presignRequest) (presignResult, error) {
	var result presignResult
	path := "/v1/storage/object/presign/" + url.PathEscape(bucket) + "/" + url.PathEscape(key)
	err := c.call(ctx, http.MethodPost, path, request, &result)
	return result, err
}

type headResult struct {
	Found bool `json:"found"`
}

func (c *storageClient) head(ctx context.Context, bucket, key string) (headResult, error) {
	var result headResult
	path := "/v1/storage/object/head/" + url.PathEscape(bucket) + "/" + url.PathEscape(key)
	err := c.call(ctx, http.MethodGet, path, nil, &result)
	return result, err
}

func (c *storageClient) call(ctx context.Context, method, path string, body, output any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}

	for attempt := 0; attempt < 4; attempt++ {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(encoded)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		response, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("transport: %w", err)
		}
		payload, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read response: %w", readErr)
		}

		var env envelope
		if err := json.Unmarshal(payload, &env); err != nil {
			return fmt.Errorf("decode envelope: %w", err)
		}
		if response.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			delay := retryDelay(response.Header.Get("Retry-After"), attempt)
			if err := c.sleep(ctx, delay); err != nil {
				return err
			}
			continue
		}
		if !env.OK {
			if env.Error == nil {
				return errors.New("Infrai request was rejected")
			}
			message := env.Error.Message
			if env.Error.Hint != "" {
				message = env.Error.Hint
			}
			return &infraiError{Code: env.Error.Code, Message: message, HTTPStatus: response.StatusCode}
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
		}
		if output != nil {
			if head, ok := output.(*headResult); ok && env.Found != nil {
				head.Found = *env.Found
			} else if err := json.Unmarshal(env.Data, output); err != nil {
				return fmt.Errorf("decode data: %w", err)
			}
		}
		return nil
	}
	return errors.New("request attempts exhausted")
}

func retryDelay(retryAfter string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Duration(1<<attempt) * 200 * time.Millisecond
}
