package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/eventloop"
	"github.com/evanw/esbuild/pkg/api"
	"github.com/jackc/pgx/v5/pgxpool"
	"cascata-backend/internal/utils"
)

type EdgeService struct{}

type EdgeResult struct {
	Status int         `json:"status"`
	Body   interface{} `json:"body"`
}

// transpileCode converts ES module syntax to Goja-compatible JavaScript using esbuild.
func transpileCode(code string) (string, error) {
	result := api.Transform(code, api.TransformOptions{
		Loader:     api.LoaderTS, // Supports standard JS and TypeScript features
		Format:     api.FormatIIFE,
		GlobalName: "__edge_fn",
		Target:     api.ES2015,
	})

	if len(result.Errors) > 0 {
		var errMsgs []string
		for _, err := range result.Errors {
			errMsgs = append(errMsgs, err.Text)
		}
		return "", fmt.Errorf("transpilation error: %s", strings.Join(errMsgs, "; "))
	}

	return string(result.Code), nil
}

// Execute runs a JavaScript/TypeScript edge function within a sandboxed Goja VM using an event loop.
func (s *EdgeService) Execute(
	ctx context.Context,
	code string,
	reqCtx map[string]interface{},
	envVars map[string]string,
	pool *pgxpool.Pool,
	timeoutMs int,
	projectSlug string,
) (*EdgeResult, error) {
	// Transpile code from ES modules to Goja-compatible format
	transpiledCode, err := transpileCode(code)
	if err != nil {
		return nil, err
	}

	var result *EdgeResult
	var runErr error
	var once sync.Once
	done := make(chan struct{})

	closeDone := func() {
		once.Do(func() {
			close(done)
		})
	}

	timeLimit := time.Duration(timeoutMs) * time.Millisecond
	loop := eventloop.NewEventLoop()
	var vm *goja.Runtime

	go func() {
		defer func() {
			if r := recover(); r != nil {
				runErr = fmt.Errorf("Execution Panic: %v", r)
				closeDone()
			}
		}()

		loop.Run(func(r *goja.Runtime) {
			vm = r

			// 1. Inject Crypto API
			r.Set("crypto", map[string]interface{}{
				"randomUUID": func() string {
					b := make([]byte, 16)
					rand.Read(b)
					return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
				},
			})

			// 2. Inject Environment and Context
			r.Set("env", envVars)
			r.Set("ctx", reqCtx)

			// 3. Inject Fetch API (asynchronous)
			r.Set("$fetch", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) < 1 {
					panic(r.ToValue("fetch requires at least 1 argument (url)"))
				}
				urlVal := call.Argument(0)
				if goja.IsUndefined(urlVal) || goja.IsNull(urlVal) {
					panic(r.ToValue("fetch first argument must be a string"))
				}
				urlStr := urlVal.String()
				if urlStr == "" {
					panic(r.ToValue("fetch first argument must be a non-empty string"))
				}

				var options map[string]interface{}
				if len(call.Arguments) > 1 {
					optVal := call.Argument(1)
					if !goja.IsUndefined(optVal) && !goja.IsNull(optVal) {
						if m, ok := optVal.Export().(map[string]interface{}); ok {
							options = m
						}
					}
				}

				// Run synchronously in the Goja thread to prevent the event loop from exiting early
				// but return a Promise to JavaScript to maintain standard fetch behavior.
				method := "GET"
				if options != nil {
					if m, ok := options["method"].(string); ok {
						method = strings.ToUpper(m)
					}
				}

				var bodyReader io.Reader
				var bodyBytes []byte
				var contentType string

				if options != nil && options["body"] != nil {
					bodyVal := options["body"]
					switch v := bodyVal.(type) {
					case string:
						bodyBytes = []byte(v)
					default:
						jsonBytes, err := json.Marshal(v)
						if err == nil {
							bodyBytes = jsonBytes
							contentType = "application/json"
						} else {
							bodyBytes = []byte(fmt.Sprintf("%v", v))
						}
					}
					bodyReader = bytes.NewReader(bodyBytes)
				}

				if options != nil && options["query"] != nil {
					if q, ok := options["query"].(map[string]interface{}); ok {
						u, err := url.Parse(urlStr)
						if err == nil {
							query := u.Query()
							for k, val := range q {
								query.Set(k, fmt.Sprintf("%v", val))
							}
							u.RawQuery = query.Encode()
							urlStr = u.String()
						}
					}
				}

				reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				if options != nil {
					if t, ok := options["timeout"].(float64); ok && t > 0 {
						reqCtx, cancel = context.WithTimeout(ctx, time.Duration(t)*time.Millisecond)
					} else if t, ok := options["timeout"].(int64); ok && t > 0 {
						reqCtx, cancel = context.WithTimeout(ctx, time.Duration(t)*time.Millisecond)
					}
				}
				defer cancel()

				req, err := http.NewRequestWithContext(reqCtx, method, urlStr, bodyReader)
				if err != nil {
					promise, _, reject := r.NewPromise()
					_ = reject(r.ToValue(fmt.Sprintf("failed to create request: %v", err)))
					return r.ToValue(promise)
				}

				if contentType != "" {
					req.Header.Set("Content-Type", contentType)
				}
				if options != nil && options["headers"] != nil {
					if headers, ok := options["headers"].(map[string]interface{}); ok {
						for k, v := range headers {
							req.Header.Set(k, fmt.Sprintf("%v", v))
						}
					}
				}

				client := &http.Client{}
				resp, err := client.Do(req)
				if err != nil {
					promise, _, reject := r.NewPromise()
					_ = reject(r.ToValue(fmt.Sprintf("request failed: %v", err)))
					return r.ToValue(promise)
				}
				defer resp.Body.Close()

				// Limit response body read to 10MB to avoid OOM
				const maxResponseBodySize = 10 * 1024 * 1024
				respBodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize))
				if err != nil {
					promise, _, reject := r.NewPromise()
					_ = reject(r.ToValue(fmt.Sprintf("failed to read response: %v", err)))
					return r.ToValue(promise)
				}

				respHeaders := make(map[string]string)
				for k, v := range resp.Header {
					if len(v) > 0 {
						respHeaders[strings.ToLower(k)] = v[0]
					}
				}

				var parsedData interface{}
				respContentType := resp.Header.Get("Content-Type")
				if strings.Contains(strings.ToLower(respContentType), "application/json") {
					var jsonVal interface{}
					if err := json.Unmarshal(respBodyBytes, &jsonVal); err == nil {
						parsedData = jsonVal
					} else {
						parsedData = string(respBodyBytes)
					}
				} else {
					parsedData = string(respBodyBytes)
				}

				result := map[string]interface{}{
					"ok":         resp.StatusCode >= 200 && resp.StatusCode < 300,
					"status":     resp.StatusCode,
					"statusText": resp.Status,
					"headers":    respHeaders,
					"data":       parsedData,
					"body":       string(respBodyBytes),
				}

				promise, resolve, _ := r.NewPromise()
				_ = resolve(r.ToValue(result))
				return r.ToValue(promise)
			})

			// 4. Inject Database Bridge
			r.Set("$db", map[string]interface{}{
				"query": func(sql string, params ...interface{}) (interface{}, error) {
					rows, err := pool.Query(ctx, sql, params...)
					if err != nil {
						return nil, err
					}
					defer rows.Close()

					var result []map[string]interface{}
					fieldDescriptions := rows.FieldDescriptions()
					for rows.Next() {
						values, _ := rows.Values()
						row := make(map[string]interface{})
						for i, fd := range fieldDescriptions {
							row[fd.Name] = utils.PurifyPgxValue(values[i])
						}
						result = append(result, row)
					}
					return result, nil
				},
			})

			// Inject helper callbacks for promise resolution
			r.Set("__edge_resolve", func(call goja.FunctionCall) goja.Value {
				val := call.Argument(0)
				result = &EdgeResult{Status: 200, Body: val.Export()}
				closeDone()
				return goja.Undefined()
			})

			r.Set("__edge_reject", func(call goja.FunctionCall) goja.Value {
				errVal := call.Argument(0)
				runErr = fmt.Errorf("Execution Error: %s", errVal.String())
				closeDone()
				return goja.Undefined()
			})

			// 5. Run the transpiled code to define __edge_fn
			_, runErr = r.RunString(transpiledCode)
			if runErr != nil {
				closeDone()
				return
			}

			// 6. Verify __edge_fn and default export exist and default is a function
			val := r.Get("__edge_fn")
			if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
				runErr = fmt.Errorf("transpiled code did not define __edge_fn")
				closeDone()
				return
			}
			obj := val.ToObject(r)
			defaultExport := obj.Get("default")
			if defaultExport == nil || goja.IsUndefined(defaultExport) || goja.IsNull(defaultExport) {
				runErr = fmt.Errorf("default export not found")
				closeDone()
				return
			}
			if _, ok := goja.AssertFunction(defaultExport); !ok {
				runErr = fmt.Errorf("default export is not a function")
				closeDone()
				return
			}

			// 7. Run the runner script
			runnerScript := `
			try {
				var res = __edge_fn.default(ctx);
				if (res && typeof res.then === 'function') {
					res.then(function(v) { __edge_resolve(v); }).catch(function(e) { __edge_reject(e); });
				} else {
					__edge_resolve(res);
				}
			} catch(err) {
				__edge_reject(err);
			}
			`
			_, runErr = r.RunString(runnerScript)
			if runErr != nil {
				closeDone()
			}
		})
	}()

	select {
	case <-done:
		loop.Stop()
		if runErr != nil {
			return nil, runErr
		}
		return result, nil
	case <-ctx.Done():
		if vm != nil {
			vm.Interrupt("request_cancelled")
		}
		loop.Stop()
		return nil, ctx.Err()
	case <-time.After(timeLimit):
		if vm != nil {
			vm.Interrupt("timeout")
		}
		loop.Stop()
		return nil, fmt.Errorf("Execution Timed Out (%dms)", timeoutMs)
	}
}
