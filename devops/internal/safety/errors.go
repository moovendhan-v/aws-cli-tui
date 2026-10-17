package safety

import (
	"context"
	"errors"
	"strings"

	"github.com/aws/smithy-go"
)

type ErrorKind string

const (
	ErrNone       ErrorKind = ""
	ErrAuth       ErrorKind = "authentication"
	ErrPermission ErrorKind = "permission"
	ErrNetwork    ErrorKind = "network"
	ErrNotFound   ErrorKind = "not found"
	ErrThrottling ErrorKind = "throttling"
	ErrTimeout    ErrorKind = "timeout"
	ErrCancelled  ErrorKind = "cancelled"
	ErrValidation ErrorKind = "validation"
	ErrConfig     ErrorKind = "configuration"
	ErrUnknown    ErrorKind = "error"
)

var (
	authCodes = []string{"ExpiredToken", "ExpiredTokenException", "InvalidClientTokenId", "UnrecognizedClientException",
		"InvalidSignatureException", "SignatureDoesNotMatch", "RequestExpired", "InvalidGrantException", "UnauthorizedClientException"}
	permCodes = []string{"AccessDenied", "AccessDeniedException", "UnauthorizedOperation", "AuthorizationError",
		"UnauthorizedException", "Forbidden"}
	throttleCodes = []string{"Throttling", "ThrottlingException", "RequestLimitExceeded", "TooManyRequestsException",
		"ProvisionedThroughputExceededException", "RequestThrottled"}
)

// Classify maps an error to a kind the UI can explain.
func Classify(err error) ErrorKind {
	if err == nil {
		return ErrNone
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrTimeout
	}
	if errors.Is(err, context.Canceled) {
		return ErrCancelled
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		code := ae.ErrorCode()
		switch {
		case contains(authCodes, code):
			return ErrAuth
		case contains(permCodes, code):
			return ErrPermission
		case contains(throttleCodes, code):
			return ErrThrottling
		case strings.Contains(code, "NotFound") || strings.Contains(code, "Malformed") || code == "ResourceNotFoundException":
			return ErrNotFound
		case strings.HasPrefix(code, "Invalid") || code == "ValidationException" || code == "InvalidParameterException":
			return ErrValidation
		}
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "sso session section"), strings.Contains(msg, "shared config"),
		strings.Contains(msg, "failed to get shared config profile"), strings.Contains(msg, "profile not found"):
		return ErrConfig
	case strings.Contains(msg, "sso") && (strings.Contains(msg, "token") || strings.Contains(msg, "expired")),
		strings.Contains(msg, "failed to refresh cached credentials"),
		strings.Contains(msg, "no valid credential sources"),
		strings.Contains(msg, "get credentials"),
		strings.Contains(msg, "failed to retrieve credentials"):
		return ErrAuth
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "dial tcp"),
		strings.Contains(msg, "connection refused"), strings.Contains(msg, "network is unreachable"),
		strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "tls handshake"):
		return ErrNetwork
	case strings.Contains(msg, "rate exceeded"):
		return ErrThrottling
	case strings.Contains(msg, "access denied"), strings.Contains(msg, "not authorized"):
		return ErrPermission
	}
	return ErrUnknown
}

// Hint returns a recovery suggestion for an error kind.
func Hint(k ErrorKind) string {
	switch k {
	case ErrAuth:
		return "Credentials are missing or expired. Press L to run `aws sso login`, or Ctrl+P to pick another profile."
	case ErrPermission:
		return "The current role is not allowed to perform this action. Check its IAM policy or switch profile."
	case ErrNetwork:
		return "AWS could not be reached. Check your network, VPN or proxy settings."
	case ErrNotFound:
		return "The resource does not exist in this account/region. Check the region (Ctrl+R)."
	case ErrThrottling:
		return "AWS is throttling requests. Wait a moment and retry."
	case ErrTimeout:
		return "The operation timed out. AWS-side work may still be in progress."
	case ErrValidation:
		return "AWS rejected the request parameters."
	case ErrConfig:
		return "This profile's entry in ~/.aws/config is incomplete or invalid. Fix it, or press Ctrl+P to choose another profile."
	}
	return ""
}

// Describe renders "kind: message" with AWS request noise trimmed.
func Describe(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	var ae smithy.APIError
	if errors.As(err, &ae) {
		msg = ae.ErrorCode() + ": " + ae.ErrorMessage()
	}
	return Redact(msg)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
