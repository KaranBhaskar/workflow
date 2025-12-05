package integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newRequest(method, target string, body io.Reader, headers map[string]string) *http.Request {
	req := httptest.NewRequest(method, target, body)
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	return req
}

func newAuthedRequest(method, target, apiKey string, body io.Reader) *http.Request {
	return newRequest(method, target, body, map[string]string{
		"X-API-Key": apiKey,
	})
}

func newAuthedJSONRequest(method, target, apiKey, body string) *http.Request {
	return newRequest(method, target, strings.NewReader(body), map[string]string{
		"Content-Type": "application/json",
		"X-API-Key":    apiKey,
	})
}

func newAuthedMultipartRequest(t *testing.T, target, apiKey, fieldName, filename string, payload []byte) *http.Request {
	t.Helper()

	body, contentType := multipartBody(t, fieldName, filename, payload)
	return newRequest(http.MethodPost, target, body, map[string]string{
		"Content-Type": contentType,
		"X-API-Key":    apiKey,
	})
}

func performRequest(handler http.Handler, req *http.Request) *http.Response {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder.Result()
}

func expectStatus(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	if resp.StatusCode != expected {
		t.Fatalf("expected %d, got %d", expected, resp.StatusCode)
	}
}

func decodeJSONResponse(t *testing.T, resp *http.Response, destination any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(destination); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
