package gateway

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

func writeGRPCError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	st, ok := status.FromError(err)
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadGateway, errorBody{Error: err.Error(), Code: "unavailable"})
		return
	}
	httpStatus := grpcCodeToHTTP(st.Code())
	c.AbortWithStatusJSON(httpStatus, errorBody{
		Error: st.Message(),
		Code:  stringsCode(st.Code()),
	})
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

func stringsCode(code codes.Code) string {
	switch code {
	case codes.InvalidArgument:
		return "invalid_argument"
	case codes.FailedPrecondition:
		return "failed_precondition"
	case codes.Unauthenticated:
		return "unauthenticated"
	case codes.PermissionDenied:
		return "permission_denied"
	case codes.NotFound:
		return "not_found"
	case codes.AlreadyExists:
		return "already_exists"
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
