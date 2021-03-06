package runtime

import (
	"io"
	"net/http"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/any"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/status"
)

var errorMap = map[codes.Code]int{
	codes.OK:                 http.StatusOK,
	codes.Canceled:           http.StatusRequestTimeout,
	codes.Unknown:            http.StatusInternalServerError,
	codes.InvalidArgument:    http.StatusBadRequest,
	codes.DeadlineExceeded:   http.StatusRequestTimeout,
	codes.NotFound:           http.StatusNotFound,
	codes.AlreadyExists:      http.StatusConflict,
	codes.PermissionDenied:   http.StatusForbidden,
	codes.Unauthenticated:    http.StatusUnauthorized,
	codes.ResourceExhausted:  http.StatusTooManyRequests,
	codes.FailedPrecondition: http.StatusPreconditionFailed,
	codes.Aborted:            http.StatusConflict,
	codes.OutOfRange:         http.StatusBadRequest,
	codes.Unimplemented:      http.StatusNotImplemented,
	codes.Internal:           http.StatusInternalServerError,
	codes.Unavailable:        http.StatusServiceUnavailable,
	codes.DataLoss:           http.StatusInternalServerError,
}

// HTTPStatusFromCode converts a gRPC error code into the corresponding HTTP response status.
func HTTPStatusFromCode(code codes.Code) int {
	if code, ok := errorMap[code]; ok {
		return code
	}
	grpclog.Printf("Unknown gRPC error code: %v", code)
	return http.StatusInternalServerError
}

var (
	// HTTPError replies to the request with the error.
	// You can set a custom function to this variable to customize error format.
	HTTPError = DefaultHTTPError
	// OtherErrorHandler handles the following error used by the gateway: StatusMethodNotAllowed StatusNotFound and StatusBadRequest
	OtherErrorHandler = DefaultOtherErrorHandler
)

type errorBody struct {
	Error   string     `protobuf:"bytes,1,name=error" json:"error"`
	Code    int32      `protobuf:"varint,2,name=code" json:"code"`
	Details []*any.Any `protobuf:"bytes,3,rep,name=details" json:"details,omitempty"`
}

// Make this also conform to proto.Message for builtin JSONPb Marshaler
func (e *errorBody) Reset()         { *e = errorBody{} }
func (e *errorBody) String() string { return proto.CompactTextString(e) }
func (*errorBody) ProtoMessage()    {}

// DefaultHTTPError is the default implementation of HTTPError.
// If "err" is an error from gRPC system, the function replies with the status code mapped by HTTPStatusFromCode.
// If otherwise, it replies with http.StatusInternalServerError.
//
// The response body returned by this function is a JSON object,
// which contains a member whose key is "error" and whose value is err.Error().
func DefaultHTTPError(ctx context.Context, mux *ServeMux, marshaler Marshaler, w http.ResponseWriter, _ *http.Request, err error) {
	const fallback = `{"error": "failed to marshal error message"}`

	s, ok := status.FromError(err)
	if !ok {
		s = status.New(codes.Unknown, err.Error())
	}

	body := &errorBody{
		Error: s.Message(),
		Code:  int32(s.Code()),
	}

	for _, detail := range s.Details() {
		if det, ok := detail.(proto.Message); ok {
			a, err := ptypes.MarshalAny(det)
			if err != nil {
				grpclog.Printf("Failed to marshal any: %v", err)
			} else {
				body.Details = append(body.Details, a)
			}
		}
	}

	w.Header().Del("Trailer")
	w.Header().Set("Content-Type", marshaler.ContentType(body))

	buf, merr := marshaler.Marshal(body)
	if merr != nil {
		grpclog.Printf("Failed to marshal error message %q: %v", body, merr)
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := io.WriteString(w, fallback); err != nil {
			grpclog.Printf("Failed to write response: %v", err)
		}
		return
	}

	md, ok := ServerMetadataFromContext(ctx)
	if !ok {
		grpclog.Printf("Failed to extract ServerMetadata from context")
	}

	handleForwardResponseServerMetadata(w, mux, md)
	handleForwardResponseTrailerHeader(w, md)
	st := HTTPStatusFromCode(s.Code())
	w.WriteHeader(st)
	if _, err := w.Write(buf); err != nil {
		grpclog.Printf("Failed to write response: %v", err)
	}

	handleForwardResponseTrailer(w, md)
}

// DefaultOtherErrorHandler is the default implementation of OtherErrorHandler.
// It simply writes a string representation of the given error into "w".
func DefaultOtherErrorHandler(w http.ResponseWriter, _ *http.Request, msg string, code int) {
	http.Error(w, msg, code)
}
