package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Server exposes the OTLP trace receiver over gRPC (:4317) and HTTP (:4318),
// matching the standard OTel Collector ports so it is a drop-in
// OTEL_EXPORTER_OTLP_ENDPOINT target.
type Server struct {
	grpcAddr string
	httpAddr string
	proc     *Processor
	log      *slog.Logger

	grpcSrv *grpc.Server
	httpSrv *http.Server
	grpcLis net.Listener
	httpLis net.Listener
	errCh   chan error
}

// NewServer constructs the receiver. Addresses are host:port (e.g. ":4317").
func NewServer(grpcAddr, httpAddr string, proc *Processor, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{grpcAddr: grpcAddr, httpAddr: httpAddr, proc: proc, log: log}
}

// grpcService adapts the Processor to the OTLP TraceService gRPC interface.
type grpcService struct {
	coltracepb.UnimplementedTraceServiceServer
	proc *Processor
}

func (g *grpcService) Export(ctx context.Context, req *coltracepb.ExportTraceServiceRequest) (*coltracepb.ExportTraceServiceResponse, error) {
	if err := g.proc.ConsumeTraces(ctx, req); err != nil {
		return nil, err
	}
	return &coltracepb.ExportTraceServiceResponse{}, nil
}

// Start binds both listeners and begins serving in background goroutines. It
// returns once the sockets are bound, so callers (and tests) can immediately
// reach the receiver at Addrs(). Use ListenAndServe for the blocking variant.
func (s *Server) Start() error {
	grpcLis, err := net.Listen("tcp", s.grpcAddr)
	if err != nil {
		return fmt.Errorf("listen gRPC %s: %w", s.grpcAddr, err)
	}
	httpLis, err := net.Listen("tcp", s.httpAddr)
	if err != nil {
		_ = grpcLis.Close()
		return fmt.Errorf("listen HTTP %s: %w", s.httpAddr, err)
	}
	s.grpcLis, s.httpLis = grpcLis, httpLis

	s.grpcSrv = grpc.NewServer()
	coltracepb.RegisterTraceServiceServer(s.grpcSrv, &grpcService{proc: s.proc})
	s.httpSrv = &http.Server{Handler: s.httpMux(), ReadHeaderTimeout: 10 * time.Second}

	s.errCh = make(chan error, 2)
	go func() {
		s.log.Info("OTLP gRPC receiver listening", "addr", grpcLis.Addr().String())
		if err := s.grpcSrv.Serve(grpcLis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			s.errCh <- fmt.Errorf("gRPC serve: %w", err)
		}
	}()
	go func() {
		s.log.Info("OTLP HTTP receiver listening", "addr", httpLis.Addr().String())
		if err := s.httpSrv.Serve(httpLis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.errCh <- fmt.Errorf("HTTP serve: %w", err)
		}
	}()
	return nil
}

// Addrs returns the bound gRPC and HTTP addresses (valid after Start).
func (s *Server) Addrs() (grpcAddr, httpAddr string) {
	if s.grpcLis != nil {
		grpcAddr = s.grpcLis.Addr().String()
	}
	if s.httpLis != nil {
		httpAddr = s.httpLis.Addr().String()
	}
	return
}

// ListenAndServe starts both listeners and blocks until ctx is cancelled or a
// listener fails fatally, then shuts down gracefully.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := s.Start(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return s.Shutdown()
	case err := <-s.errCh:
		_ = s.Shutdown()
		return err
	}
}

// Shutdown gracefully stops both servers.
func (s *Server) Shutdown() error {
	if s.grpcSrv != nil {
		s.grpcSrv.GracefulStop()
	}
	if s.httpSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.httpSrv.Shutdown(ctx)
	}
	return nil
}

func (s *Server) httpMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", s.handleTraces)
	return mux
}

// handleTraces accepts OTLP/HTTP trace exports (protobuf or JSON) on /v1/traces.
func (s *Server) handleTraces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20)) // 32 MiB cap
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	ct := r.Header.Get("Content-Type")
	isJSON := strings.Contains(ct, "json")

	req := &coltracepb.ExportTraceServiceRequest{}
	if isJSON {
		err = protojson.Unmarshal(body, req)
	} else {
		err = proto.Unmarshal(body, req)
	}
	if err != nil {
		http.Error(w, "decode OTLP: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.proc.ConsumeTraces(r.Context(), req); err != nil {
		http.Error(w, "process: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := &coltracepb.ExportTraceServiceResponse{}
	if isJSON {
		out, _ := protojson.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
		return
	}
	out, _ := proto.Marshal(resp)
	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(out)
}
