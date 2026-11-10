package otlpingest

import (
	"encoding/json"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// gRPC status codes carried in the google.rpc.Status body OTLP/HTTP uses for
// failures.
const (
	CodeInvalidArgument   = 3
	CodeResourceExhausted = 8
	CodeUnauthenticated   = 16
	CodeUnavailable       = 14
	CodePermissionDenied  = 7
	CodeInternal          = 13
)

// EncodeStatus renders a google.rpc.Status (code, message) in the content
// kind of the request: protobuf wire format or JSON.
func EncodeStatus(kind string, code int32, message string) (contentType string, body []byte) {
	if kind == KindJSON {
		body, _ = json.Marshal(struct {
			Code    int32  `json:"code"`
			Message string `json:"message"`
		}{code, message})
		return "application/json", body
	}
	var b []byte
	if code != 0 {
		b = protowire.AppendTag(b, 1, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(uint32(code)))
	}
	if message != "" {
		b = protowire.AppendTag(b, 2, protowire.BytesType)
		b = protowire.AppendString(b, message)
	}
	return "application/x-protobuf", b
}

// EncodeSuccess renders the signal's export response. rejected is the number
// of records (log records, data points or spans) the receiver did not accept,
// reported as OTLP partial success; zero reports none.
func EncodeSuccess(kind string, signal Signal, rejected int64, message string) (contentType string, body []byte, err error) {
	var msg proto.Message
	switch signal {
	case SignalLogs:
		r := &collogs.ExportLogsServiceResponse{}
		if rejected > 0 {
			r.PartialSuccess = &collogs.ExportLogsPartialSuccess{RejectedLogRecords: rejected, ErrorMessage: message}
		}
		msg = r
	case SignalMetrics:
		r := &colmetrics.ExportMetricsServiceResponse{}
		if rejected > 0 {
			r.PartialSuccess = &colmetrics.ExportMetricsPartialSuccess{RejectedDataPoints: rejected, ErrorMessage: message}
		}
		msg = r
	default:
		r := &coltrace.ExportTraceServiceResponse{}
		if rejected > 0 {
			r.PartialSuccess = &coltrace.ExportTracePartialSuccess{RejectedSpans: rejected, ErrorMessage: message}
		}
		msg = r
	}
	if kind == KindJSON {
		body, err = protojson.MarshalOptions{UseProtoNames: false}.Marshal(msg)
		return "application/json", body, err
	}
	body, err = proto.Marshal(msg)
	return "application/x-protobuf", body, err
}
