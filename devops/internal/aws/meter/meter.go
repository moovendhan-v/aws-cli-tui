// Package meter counts and rate-limits every AWS request the tool makes, and
// flags operations that are billed per call or per unit of data.
package meter

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	awsmw "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/smithy-go/middleware"
	"golang.org/x/time/rate"
)

// ErrBudget is returned once the session API call budget is exhausted.
var ErrBudget = errors.New("session API call budget reached (limits.max_api_calls in Settings)")

// billed lists operations charged per request or per data unit. Prices are
// deliberately not hard-coded here; see the AWS pricing page for each service.
var billed = map[string]string{
	"CloudWatch:GetMetricData":       "CloudWatch metrics API requests are billed per metric requested",
	"CloudWatch:GetMetricStatistics": "CloudWatch metrics API requests are billed per request",
	"CloudWatch Logs:StartLiveTail":  "Live Tail is billed per session minute",
	"CloudWatch Logs:StartQuery":     "Logs Insights is billed per GB scanned",
	"S3:GetObject":                   "S3 GET requests and data transfer out are billed",
	"Cost Explorer:GetCostAndUsage":  "Cost Explorer API requests are billed per request",
}

// BilledNote returns the billing note for service:operation, or "".
func BilledNote(service, op string) string { return billed[service+":"+op] }

type Limits struct {
	RequestsPerSecond float64 // per service; 0 = unlimited
	WarnCalls         int     // 0 = never warn
	MaxCalls          int     // 0 = no hard cap
}

type Stat struct {
	Service, Operation string
	Calls              int
	Billed             bool
}

type Meter struct {
	mu       sync.Mutex
	limits   Limits
	calls    map[string]int
	total    int
	billed   int
	started  time.Time
	limiters map[string]*rate.Limiter
	warned   bool
	onWarn   func(string)
}

var Default = New(Limits{RequestsPerSecond: 10})

func New(l Limits) *Meter {
	return &Meter{limits: l, calls: map[string]int{}, limiters: map[string]*rate.Limiter{}, started: time.Now()}
}

func (m *Meter) SetLimits(l Limits) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limits = l
	m.limiters = map[string]*rate.Limiter{}
}

// OnWarn registers a callback fired once when WarnCalls is crossed.
func (m *Meter) OnWarn(f func(string)) {
	m.mu.Lock()
	m.onWarn = f
	m.mu.Unlock()
}

func (m *Meter) limiter(service string) *rate.Limiter {
	if m.limits.RequestsPerSecond <= 0 {
		return nil
	}
	l, ok := m.limiters[service]
	if !ok {
		burst := max(1, int(m.limits.RequestsPerSecond))
		l = rate.NewLimiter(rate.Limit(m.limits.RequestsPerSecond), burst)
		m.limiters[service] = l
	}
	return l
}

// before checks the budget, records the call and returns the limiter to wait on.
func (m *Meter) before(service, op string) (*rate.Limiter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.limits.MaxCalls > 0 && m.total >= m.limits.MaxCalls {
		return nil, ErrBudget
	}
	m.total++
	m.calls[service+":"+op]++
	if billed[service+":"+op] != "" {
		m.billed++
	}
	if m.limits.WarnCalls > 0 && m.total >= m.limits.WarnCalls && !m.warned {
		m.warned = true
		if f := m.onWarn; f != nil {
			go f(fmt.Sprintf("⚠ %d AWS API calls this session", m.total))
		}
	}
	return m.limiter(service), nil
}

// Totals returns total calls and billed calls this session.
func (m *Meter) Totals() (total, billedCalls int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.total, m.billed
}

// Stats returns per-operation counts, busiest first.
func (m *Meter) Stats() []Stat {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Stat, 0, len(m.calls))
	for k, n := range m.calls {
		svc, op, _ := strings.Cut(k, ":")
		out = append(out, Stat{Service: svc, Operation: op, Calls: n, Billed: billed[k] != ""})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Calls > out[j].Calls })
	return out
}

// Middleware counts and rate-limits each HTTP attempt (after retries are
// scheduled, so retries count too — that is what hits AWS quotas).
func (m *Meter) Middleware() func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Finalize.Add(middleware.FinalizeMiddlewareFunc("devopsMeter",
			func(ctx context.Context, in middleware.FinalizeInput, next middleware.FinalizeHandler) (middleware.FinalizeOutput, middleware.Metadata, error) {
				lim, err := m.before(awsmw.GetServiceID(ctx), awsmw.GetOperationName(ctx))
				if err != nil {
					return middleware.FinalizeOutput{}, middleware.Metadata{}, err
				}
				if lim != nil {
					if err := lim.Wait(ctx); err != nil {
						return middleware.FinalizeOutput{}, middleware.Metadata{}, err
					}
				}
				return next.HandleFinalize(ctx, in)
			}), middleware.After)
	}
}
