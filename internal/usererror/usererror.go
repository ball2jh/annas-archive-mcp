// Package usererror defines stable, user-facing tool errors.
package usererror

import (
	"errors"
	"fmt"
	"slices"
)

// Stable error codes. These are part of the public tool contract: agents match
// on them, so existing values must not change.
const (
	// Input validation.
	CodeInvalidQuery       = "INVALID_QUERY"
	CodeInvalidContentType = "INVALID_CONTENT_TYPE"
	CodeInvalidHash        = "INVALID_HASH"
	CodeInvalidDOI         = "INVALID_DOI"

	// Configuration and filesystem.
	CodeConfig          = "CONFIG"
	CodeAuthRequired    = "AUTH_REQUIRED"
	CodeAuthInvalid     = "AUTH_INVALID"
	CodePathRequired    = "PATH_REQUIRED"
	CodePathUnavailable = "PATH_UNAVAILABLE"
	CodeIOError         = "IO_ERROR"

	// Rate limits and quotas.
	CodeRateLimited      = "RATE_LIMITED"
	CodeLocalRateLimited = "LOCAL_RATE_LIMITED"
	CodeQuotaExhausted   = "QUOTA_EXHAUSTED"

	// Transport.
	CodeRequestTimeout   = "REQUEST_TIMEOUT"
	CodeRequestCancelled = "REQUEST_CANCELLED"

	// Upstream.
	CodeUpstreamBlocked     = "UPSTREAM_BLOCKED"
	CodeUpstreamRejected    = "UPSTREAM_REJECTED"
	CodeUpstreamUnavailable = "UPSTREAM_UNAVAILABLE"
	CodeUpstreamApiError    = "UPSTREAM_API_ERROR"
	CodeNotFound            = "NOT_FOUND"
	CodeNotFoundOnLibgen    = "NOT_FOUND_ON_LIBGEN"
	CodeNotFoundOnScinet    = "NOT_FOUND_ON_SCINET"
	CodeNotFastDownloadable = "NOT_FAST_DOWNLOADABLE"

	// Unclassified failures, one per tool.
	CodeSearchFailed      = "SEARCH_FAILED"
	CodeDownloadFailed    = "DOWNLOAD_FAILED"
	CodeDOIDownloadFailed = "DOI_DOWNLOAD_FAILED"
	CodeDOILookupFailed   = "DOI_LOOKUP_FAILED"
	CodeDetailsFailed     = "DETAILS_FAILED"
	CodeToolFailed        = "TOOL_FAILED"
)

// Error is an error that is safe to return to an MCP tool caller.
type Error struct {
	Code    string
	Message string
	Cause   error
}

// New creates a user-facing error with a stable code.
func New(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Wrap creates a user-facing error that preserves the internal cause for logs
// and errors.As/errors.Is checks.
func Wrap(code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// HasCode reports whether err is, or wraps, an *Error with one of the codes.
func HasCode(err error, codes ...string) bool {
	var ue *Error
	if !errors.As(err, &ue) {
		return false
	}
	return slices.Contains(codes, ue.Code)
}
