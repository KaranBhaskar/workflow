package health

import (
	"context"
	"net"
	"time"
)

const (
	StatusLive     = "ok"
	StatusReady    = "ready"
	StatusNotReady = "not_ready"
)

type Checker interface {
	Name() string
	Check(context.Context) error
}

type CheckResult struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

type Report struct {
	Service   string        `json:"service"`
	Status    string        `json:"status"`
	Timestamp time.Time     `json:"timestamp"`
	Checks    []CheckResult `json:"checks,omitempty"`
}

type Service struct {
	serviceName string
	timeout     time.Duration
	checkers    []Checker
}

type TCPChecker struct {
	name    string
	address string
}

func NewService(serviceName string, timeout time.Duration, checkers ...Checker) *Service {
	return &Service{
		serviceName: serviceName,
		timeout:     timeout,
		checkers:    checkers,
	}
}

func NewTCPChecker(name, address string) *TCPChecker {
	return &TCPChecker{
		name:    name,
		address: address,
	}
}

func (c *TCPChecker) Name() string {
	return c.name
}

func (c *TCPChecker) Check(ctx context.Context) error {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", c.address)
	if err != nil {
		return err
	}

	return conn.Close()
}

func (s *Service) LiveReport() Report {
	return Report{
		Service:   s.serviceName,
		Status:    StatusLive,
		Timestamp: time.Now().UTC(),
	}
}

func (s *Service) ReadyReport(ctx context.Context) (Report, bool) {
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}

	report := Report{
		Service:   s.serviceName,
		Status:    StatusReady,
		Timestamp: time.Now().UTC(),
		Checks:    make([]CheckResult, 0, len(s.checkers)),
	}

	ready := true
	for _, checker := range s.checkers {
		start := time.Now()
		err := checker.Check(ctx)
		result := CheckResult{
			Name:       checker.Name(),
			Status:     StatusReady,
			DurationMS: time.Since(start).Milliseconds(),
		}

		if err != nil {
			ready = false
			report.Status = StatusNotReady
			result.Status = StatusNotReady
			result.Error = err.Error()
		}

		report.Checks = append(report.Checks, result)
	}

	return report, ready
}
