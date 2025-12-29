package testutil

import (
	"context"
	"errors"
	"net/http"
	"time"
)

func WaitForHTTP200(ctx context.Context, url string) error {
	return WaitForHTTPStatus(ctx, url, 200)
}

func WaitForHTTPStatus(ctx context.Context, url string, status int) error {
	if ctx == nil {
		return errors.New("wait http: ctx is nil")
	}

	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == status {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
