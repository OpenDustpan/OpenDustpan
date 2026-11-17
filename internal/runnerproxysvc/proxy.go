package runnerproxysvc

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/jobproxy"
	"opendustpan.org/opendustpan/pkg/launcher/proxy"
	"opendustpan.org/opendustpan/pkg/runner"
)

// TunnelRuleID is the ID of the built-in tunnel rule of the server host.
const TunnelRuleID = "builtin-server"

// Claim is what the runtime starts a job proxy for.
type Claim struct {
	// ID is unique for the claim on its coordinator.
	ID string
	// Job, Phase and Unit name the claim in each record.
	Job   string
	Phase string
	Unit  string
	// Operation is the operation of the phase.
	Operation jobproxy.Operation
	// Isolation is where the coordinator runs the claim.
	Isolation runner.Isolation
	// Rules is the number of proxy rules in the joined set of the unit.
	// Zero means the unit has no proxy rules.
	Rules int
}

// ServerConfig is the configuration of the job proxy of one claim.
type ServerConfig struct {
	// Claim is the claim that the proxy serves.
	Claim Claim
	// Rendered is the rendered proxy rule set of the unit.
	Rendered jobproxy.Rendered
	// ServerHost and ServerPort name the host that the built-in tunnel rule
	// allows. A zero port takes the default port of a proxy rule.
	ServerHost string
	ServerPort int
	// Egress is the outbound guard.
	Egress egress.Rules
	// Records takes the records, including the blocked lines. It is
	// required.
	Records proxy.Records
	// Resolver resolves a rule host. Nil means net.DefaultResolver.
	Resolver proxy.Resolver
	// SystemRoots are the roots of the server trust "system". Nil means the
	// roots of the system.
	SystemRoots *x509.CertPool
	// CALifetime is the lifetime of the CA of the claim. It is required.
	CALifetime time.Duration
	// Limits are the limits of the proxy. A zero field takes its default.
	Limits proxy.Limits
	// Listen is the address of the proxy. Empty takes a loopback address
	// with a free port.
	Listen string
}

// Server is the job proxy of one claim. Only NewServer makes one, and it
// rejects each configuration that would skip the outbound guard or the
// tunnel rule.
type Server struct {
	cfg      ServerConfig
	proxy    *proxy.Proxy
	mu       sync.Mutex
	listen   net.Listener
	done     chan struct{}
	closed   bool
	serveErr error
}

// NewServer returns the job proxy of one claim. It adds the built-in tunnel
// rule for the server host to the rendered proxy rules, so a caller cannot
// make a proxy that refuses the launcher and the tool.
func NewServer(cfg ServerConfig) (*Server, error) {
	place, err := Plan(cfg.Claim.Isolation, cfg.Claim.Rules)
	if err != nil {
		return nil, err
	}

	if place.Proxies != 1 {
		return nil, fmt.Errorf("%w: the claim has no proxy rules", ErrConfig)
	}

	switch {
	case cfg.Records == nil:
		return nil, fmt.Errorf("%w: the record sink", ErrConfig)
	case !cfg.Claim.Operation.Valid():
		return nil, fmt.Errorf("%w: the operation", ErrConfig)
	case cfg.ServerHost == "":
		return nil, fmt.Errorf("%w: the server host", ErrConfig)
	case cfg.CALifetime <= 0:
		return nil, fmt.Errorf("%w: the CA lifetime", ErrConfig)
	}

	rendered, err := withTunnel(cfg.Rendered, cfg.ServerHost, cfg.ServerPort)
	if err != nil {
		return nil, err
	}

	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:0"
	}

	job, err := proxy.New(proxy.Config{
		Rendered: rendered, Operation: cfg.Claim.Operation,
		Job: cfg.Claim.Job, Phase: cfg.Claim.Phase, Unit: cfg.Claim.Unit,
		Egress: cfg.Egress, Credentials: nil, Trust: nil, SystemRoots: cfg.SystemRoots,
		Records: cfg.Records, RecordBuffer: 0, RecordRetry: 0, Resolver: cfg.Resolver,
		CALifetime: cfg.CALifetime, Now: nil, Limits: cfg.Limits,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConfig, err)
	}

	return &Server{
		cfg: cfg, proxy: job, mu: sync.Mutex{}, listen: nil, done: nil, closed: false, serveErr: nil,
	}, nil
}

// withTunnel joins the rendered proxy rules with one tunnel rule for host and
// port, and renders the result again. The join refuses a user proxy rule
// with the ID of the tunnel rule or another mode for the same host and port.
func withTunnel(rendered jobproxy.Rendered, host string, port int) (jobproxy.Rendered, error) {
	if port == 0 {
		port = jobproxy.DefaultPort
	}

	rules := rendered.Rules()
	user := jobproxy.RuleSet{Rules: make([]jobproxy.Rule, 0, len(rules))}

	for i := range rules {
		user.Rules = append(user.Rules, ruleOf(&rules[i]))
	}

	tunnel := jobproxy.RuleSet{Rules: []jobproxy.Rule{{
		ID: TunnelRuleID,
		Operations: []jobproxy.Operation{
			jobproxy.OperationPlan, jobproxy.OperationApply, jobproxy.OperationDestroy, jobproxy.OperationRefresh,
		},
		Host: host, Port: port, Mode: jobproxy.ModeTunnel,
		Methods: nil, Paths: nil, Credential: "", ClientCertificate: "",
		ServerTrust: jobproxy.ServerTrustSystem,
	}}}

	joined, err := jobproxy.Join(user, tunnel)
	if err != nil {
		return jobproxy.Rendered{}, fmt.Errorf("%w: %w", ErrConfig, err)
	}

	out, err := jobproxy.Render(joined, jobproxy.Vars{
		Tool: "", Namespace: "", Name: "", Labels: nil, Operation: "", Inputs: nil, Profiles: nil,
	})
	if err != nil {
		return jobproxy.Rendered{}, fmt.Errorf("%w: %w", ErrConfig, err)
	}

	return out, nil
}

// ruleOf returns the proxy rule of a rendered proxy rule.
func ruleOf(rendered *jobproxy.RenderedRule) jobproxy.Rule {
	return jobproxy.Rule{
		ID: rendered.ID, Operations: slices.Clone(rendered.Operations), Host: rendered.Host, Port: rendered.Port,
		Mode: rendered.Mode, Methods: slices.Clone(rendered.Methods), Paths: slices.Clone(rendered.Paths),
		Credential: rendered.Credential, ClientCertificate: rendered.ClientCertificate,
		ServerTrust: rendered.ServerTrust,
	}
}

// Start listens on the configured address and serves the proxy until Close.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()

	if s.closed || s.listen != nil {
		s.mu.Unlock()

		return fmt.Errorf("%w: the proxy already ran", ErrStart)
	}
	s.mu.Unlock()

	var lc net.ListenConfig

	listen, err := lc.Listen(ctx, "tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStart, err)
	}

	done := make(chan struct{})

	s.mu.Lock()
	s.listen = listen
	s.done = done
	s.mu.Unlock()

	go func() {
		defer close(done)

		if serveErr := s.proxy.Serve(listen); serveErr != nil {
			s.mu.Lock()
			s.serveErr = serveErr
			s.mu.Unlock()
		}
	}()

	return nil
}

// Addr returns the address of the proxy, or an empty string before Start.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listen == nil {
		return ""
	}

	return s.listen.Addr().String()
}

// Done returns the channel that closes when the proxy stops.
func (s *Server) Done() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.done
}

// Err returns the error of the listener, or nil.
func (s *Server) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.serveErr
}

// CACertificatePEM returns the PEM of the CA certificate of the claim. The
// key never leaves the proxy.
func (s *Server) CACertificatePEM() []byte {
	return s.proxy.CACertificatePEM()
}

// Close stops the proxy and waits for its connections and its record sink.
// A second call does nothing.
func (s *Server) Close() error {
	s.mu.Lock()

	if s.closed {
		s.mu.Unlock()

		return nil
	}

	s.closed = true
	listen, done := s.listen, s.done
	s.mu.Unlock()

	err := s.proxy.Close()

	if listen != nil {
		if cerr := listen.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
			err = errors.Join(err, cerr)
		}
	}

	if done != nil {
		<-done
	}

	if err != nil {
		return fmt.Errorf("%w: %w", ErrClose, err)
	}

	return nil
}
