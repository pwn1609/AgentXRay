package ingest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// DecodeTracesJSON parses an OTLP/JSON ExportTraceServiceRequest (as produced by
// protojson — bytes fields such as traceId are base64). Used for test fixtures
// and the `replay` command.
func DecodeTracesJSON(data []byte) (*coltracepb.ExportTraceServiceRequest, error) {
	req := &coltracepb.ExportTraceServiceRequest{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, req); err != nil {
		return nil, fmt.Errorf("decode OTLP/JSON: %w", err)
	}
	return req, nil
}

// PostTraces sends an export request to an OTLP/HTTP endpoint over protobuf.
// endpoint is a base URL (e.g. "http://localhost:4318"); "/v1/traces" is appended.
func PostTraces(ctx context.Context, endpoint string, req *coltracepb.ExportTraceServiceRequest) error {
	body, err := proto.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal traces: %w", err)
	}
	url := strings.TrimRight(endpoint, "/") + "/v1/traces"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("post traces: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("OTLP endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
