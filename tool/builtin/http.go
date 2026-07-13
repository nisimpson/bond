package builtin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	bond "github.com/nisimpson/bond"
	"github.com/nisimpson/bond/tool"
)

// maxResponseBody is the maximum response body size (5 MB).
const maxResponseBody = 5 * 1024 * 1024

// newHTTPTool creates an HTTP fetch tool with the given default timeout.
// If defaultTimeout is zero, a 30-second default is used.
func newHTTPTool(defaultTimeout time.Duration) bond.Tool {
	if defaultTimeout == 0 {
		defaultTimeout = 30 * time.Second
	}

	t, _ := bond.NewFuncTool(
		func(ctx context.Context, input HTTPInput) (HTTPOutput, error) {
			// Requirements: TBOX-2.1, TBOX-2.2, TBOX-2.3, TBOX-2.4, TBOX-2.5, TBOX-2.6, TBOX-2.7, TBOX-2.8, TBOX-2.9, TBOX-2.10

			// Requirement: TBOX-2.3 — validate method is GET or POST
			method := strings.ToUpper(input.Method)
			if method != http.MethodGet && method != http.MethodPost {
				return HTTPOutput{}, fmt.Errorf("unsupported method %q: only GET and POST are supported: %w", input.Method, ErrValidation)
			}

			// Requirement: TBOX-2.7 — determine timeout
			timeout := defaultTimeout
			if input.TimeoutSeconds != nil && *input.TimeoutSeconds > 0 {
				timeout = time.Duration(*input.TimeoutSeconds) * time.Second
			}

			// Requirement: TBOX-2.6 — create timeout context
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			// Requirement: TBOX-2.5 — build request body for POST
			var bodyReader io.Reader
			if method == http.MethodPost && input.Body != "" {
				bodyReader = strings.NewReader(input.Body)
			}

			// Create HTTP request.
			req, err := http.NewRequestWithContext(ctx, method, input.URL, bodyReader)
			if err != nil {
				return HTTPOutput{}, fmt.Errorf("failed to create request: %w", err)
			}

			// Requirement: TBOX-2.4 — set headers
			for key, value := range input.Headers {
				req.Header.Set(key, value)
			}

			// Requirement: TBOX-2.1, TBOX-2.8 — execute request
			resp, err := (&http.Client{}).Do(req)
			if err != nil {
				// Requirement: TBOX-2.6, TBOX-2.9 — distinguish timeout from connection errors
				if ctx.Err() == context.DeadlineExceeded {
					return HTTPOutput{}, fmt.Errorf("request timed out after %s: %w", timeout, ErrTimeout)
				}
				return HTTPOutput{}, fmt.Errorf("%w: %v", ErrConnection, err)
			}
			defer resp.Body.Close()

			// Requirement: TBOX-2.10 — read response body, truncate at 5 MB
			body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
			if err != nil {
				return HTTPOutput{}, fmt.Errorf("failed to read response body: %w", err)
			}

			truncated := len(body) > maxResponseBody
			if truncated {
				body = body[:maxResponseBody]
			}

			return HTTPOutput{
				StatusCode: resp.StatusCode,
				Body:       string(body),
				Truncated:  truncated,
			}, nil
		},
		bond.FuncToolOptions{
			Name:        ToolHTTPFetch,
			Description: "Perform an HTTP request (GET or POST) and return the response status code and body.",
			InputSchema: tool.SchemaFor[HTTPInput](),
		},
	)
	return t
}
