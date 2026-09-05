package gateway

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// errorBody is the msb-cloud nested error envelope.
type errorBody struct {
	Error errorDetails `json:"error"`
}

type errorDetails struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(c *gin.Context, httpStatus int, code, message string) {
	c.AbortWithStatusJSON(httpStatus, errorBody{
		Error: errorDetails{Code: code, Message: message},
	})
}

func writeGRPCError(c *gin.Context, err error) {
	writeGRPCErrorKind(c, err, "")
}

// writeGRPCErrorKind maps a gRPC error to the cloud nested error body.
// kind is "sandbox", "volume", or "volume_file" to pick not-found codes.
func writeGRPCErrorKind(c *gin.Context, err error, kind string) {
	if err == nil {
		return
	}
	st, ok := status.FromError(err)
	if !ok {
		writeError(c, http.StatusBadGateway, "unavailable", err.Error())
		return
	}
	httpStatus := grpcCodeToHTTP(st.Code())
	writeError(c, httpStatus, cloudErrorCode(st.Code(), st.Message(), kind), st.Message())
}

func cloudErrorCode(code codes.Code, message, kind string) string {
	lower := strings.ToLower(message)
	switch code {
	case codes.NotFound:
		switch kind {
		case "volume":
			return "volume_not_found"
		case "volume_file":
			return "volume_file_not_found"
		default:
			if strings.Contains(lower, "volume") {
				return "volume_not_found"
			}
			if strings.Contains(lower, "file") || strings.Contains(lower, "no such") {
				return "volume_file_not_found"
			}
			return "sandbox_not_found"
		}
	case codes.AlreadyExists:
		return "name_already_exists"
	case codes.InvalidArgument:
		if strings.Contains(lower, "spec") || strings.Contains(lower, "config") {
			return "invalid_sandbox_config"
		}
		if strings.Contains(lower, "volume") && strings.Contains(lower, "path") {
			return "invalid_volume_path"
		}
		return "invalid_request"
	case codes.FailedPrecondition:
		if strings.Contains(lower, "spec") || strings.Contains(lower, "config") {
			return "invalid_sandbox_config"
		}
		return "invalid_request"
	case codes.Unauthenticated:
		return "unauthenticated"
	case codes.PermissionDenied:
		return "permission_denied"
	case codes.ResourceExhausted:
		return "resource_exhausted"
	case codes.Unimplemented:
		return "unimplemented"
	case codes.Unavailable:
		return "unavailable"
	case codes.DeadlineExceeded:
		return "deadline_exceeded"
	case codes.Canceled:
		return "canceled"
	default:
		return code.String()
	}
}

func grpcCodeToHTTP(code codes.Code) int {
	switch code {
	case codes.OK:
		return http.StatusOK
	case codes.InvalidArgument, codes.FailedPrecondition, codes.OutOfRange:
		return http.StatusBadRequest
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.NotFound:
		return http.StatusNotFound
	case codes.AlreadyExists, codes.Aborted:
		return http.StatusConflict
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.Unimplemented:
		return http.StatusNotImplemented
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.DeadlineExceeded, codes.Canceled:
		return http.StatusGatewayTimeout
	default:
		return http.StatusBadGateway
	}
}
