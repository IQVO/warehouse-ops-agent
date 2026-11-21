package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// The fleet REST guideline's Idempotency-Key contract (section 2) on the act
// slice's POST routes, ADR 0030:
//
//   - a missing key is 400 idempotency-key-required; a key that is not 1-128
//     printable ASCII characters starting and ending with a visible character
//     (or a repeated header) is 400 idempotency-key-invalid;
//   - fingerprint = method + path + SHA-256 of the body;
//   - the same key with the same fingerprint replays the stored answer
//     (status, headers, body) plus Idempotency-Replayed: true, without running
//     the handler; with another fingerprint it is 409 idempotency-key-reused;
//   - 2xx answers are stored, and a 4xx is stored only when its problem type
//     declares FINAL or OUTCOME on the catalogue entry (ADR 0033; the error
//     writer marks the capture with the class). A RETRYABLE refusal — and any
//     refusal with no class, the safe fallback — is NOT stored: the key is
//     released, exactly like a 5xx or a panic, so the retry executes again.
//     A released key never compares fingerprints again: a different body
//     under it is a NEW request, not idempotency-key-reused.
const (
	idempotencyKeyHeader      = "Idempotency-Key"
	idempotencyReplayedHeader = "Idempotency-Replayed"
	maxIdempotencyKeyLength   = 128
	idempotencyNotConfigured  = "idempotency key store not configured"
)

// validIdempotencyKey reports whether key is 1-128 printable ASCII characters
// (0x20-0x7E) whose first and last character are visible (0x21-0x7E). The
// spec pattern adds the trailing OWS the HTTP stack strips before this runs.
func validIdempotencyKey(key string) bool {
	if key == "" || len(key) > maxIdempotencyKeyLength {
		return false
	}
	if !visibleASCII(key[0]) || !visibleASCII(key[len(key)-1]) {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < ' ' || key[i] > '~' {
			return false
		}
	}
	return true
}

// visibleASCII reports whether b is a visible (non-space) printable ASCII byte.
func visibleASCII(b byte) bool { return b > ' ' && b <= '~' }

// fingerprint is method + path + SHA-256 of the body, itself hashed.
func fingerprint(r *http.Request, body []byte) string {
	sum := sha256.Sum256(body)
	all := sha256.Sum256([]byte(r.Method + "\n" + r.URL.EscapedPath() + "\n" + hex.EncodeToString(sum[:])))
	return hex.EncodeToString(all[:])
}

// requireIdempotencyKey is the route-scoped middleware of every keyed POST.
// A nil store answers 503: the composition root always wires one.
func requireIdempotencyKey(store ports.IdempotencyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, ok := readIdempotencyKey(w, r)
			if !ok {
				return
			}
			if store == nil {
				writeProblem(w, r, http.StatusServiceUnavailable, idempotencyNotConfigured)
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, maxActBodyBytes+1))
			if err != nil || len(body) > maxActBodyBytes {
				writeProblem(w, r, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			_ = r.Body.Close()

			var unstored ports.StoredResponse
			resp, replayed, err := store.Execute(r.Context(), key, fingerprint(r, body),
				func(ctx context.Context) (ports.StoredResponse, error) {
					out := capture(next, r.WithContext(ctx), body)
					stored := ports.StoredResponse{Status: out.Code, Header: out.Header().Clone(), Body: out.Body.Bytes()}
					if !out.storable() {
						// A 5xx, a panic-poisoned answer or a RETRYABLE
						// refusal (ADR 0033): the key is released and the
						// captured answer is flushed unstored.
						unstored = stored
						return stored, ports.ErrResponseNotStored
					}
					return stored, nil
				})
			switch {
			case errors.Is(err, ports.ErrResponseNotStored):
				resp = unstored
			case errors.Is(err, ports.ErrIdempotencyKeyReused):
				writeKeyReused(w, r)
				return
			case err != nil:
				// Only the waiter's own context can fail here (client gone).
				writeProblem(w, r, http.StatusServiceUnavailable, "request cancelled while an earlier request with the same "+idempotencyKeyHeader+" was running")
				return
			}
			writeStored(w, resp, replayed)
		})
	}
}

// The three Idempotency-Key problems the middleware answers itself, before
// the store is touched (so never stored). They are also catalogue entries
// (act_problems.go) with their replay class declared there.
func writeKeyRequired(w http.ResponseWriter, r *http.Request) {
	writeTypedProblem(w, r, http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required",
		"send an "+idempotencyKeyHeader+" header of 1 to 128 printable ASCII characters (a client-generated UUID per logical submit; reuse it on retry)")
}

func writeKeyInvalid(w http.ResponseWriter, r *http.Request) {
	writeTypedProblem(w, r, http.StatusBadRequest, "idempotency-key-invalid", "Idempotency-Key header is invalid",
		"the "+idempotencyKeyHeader+" header must be one value of 1 to 128 printable ASCII characters that starts and ends with a visible character")
}

func writeKeyReused(w http.ResponseWriter, r *http.Request) {
	writeTypedProblem(w, r, http.StatusConflict, "idempotency-key-reused",
		"Idempotency-Key was already used with a different request",
		"the "+idempotencyKeyHeader+" was first used with a different method, path or body; use a new key for a different request")
}

// readIdempotencyKey validates the header; it answers the 400 itself.
func readIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	keys := r.Header.Values(idempotencyKeyHeader)
	if len(keys) == 0 {
		writeKeyRequired(w, r)
		return "", false
	}
	if len(keys) > 1 || !validIdempotencyKey(keys[0]) {
		writeKeyInvalid(w, r)
		return "", false
	}
	return keys[0], true
}

// captureWriter is the recorder the middleware runs the handler against. It
// carries the replay class the production error writer marks on it
// (replay_class.go): the class, with the status, decides storable().
type captureWriter struct {
	*httptest.ResponseRecorder
	class replayClass
}

// markReplayClass implements replayMarker; the error writers call it through
// markReplayClass(w, class), a no-op on any other writer.
func (c *captureWriter) markReplayClass(class replayClass) { c.class = class }

// storable reports whether the captured answer is final for the request: a
// 2xx is stored, a 5xx never is, and any other answer is stored only when its
// problem type declared FINAL or OUTCOME. A refusal that reached the capture
// with no class (a writer that forgot the catalogue) is released like
// RETRYABLE: a released refusal costs one more execution, a wrongly stored
// one pins a stale refusal for the whole retention window (ADR 0033).
func (c *captureWriter) storable() bool {
	switch {
	case c.Code >= http.StatusOK && c.Code < http.StatusMultipleChoices:
		return true
	case c.Code >= http.StatusInternalServerError:
		return false
	default:
		return c.class == classFinal || c.class == classOutcome
	}
}

// capture runs next against a captureWriter, replaying body as the request
// body.
func capture(next http.Handler, req *http.Request, body []byte) *captureWriter {
	req.Body = io.NopCloser(bytes.NewReader(body))
	rec := &captureWriter{ResponseRecorder: httptest.NewRecorder()}
	next.ServeHTTP(rec, req)
	return rec
}

// writeStored writes a first answer or a replay of one.
func writeStored(w http.ResponseWriter, resp ports.StoredResponse, replayed bool) {
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if replayed {
		w.Header().Set(idempotencyReplayedHeader, "true")
	}
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body)
}
