package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"loom/internal/auth"
	"loom/internal/cassette"
	"loom/internal/egress"
	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/host"
	"loom/internal/pyenv"
	"loom/internal/server"
	"loom/internal/store"
	"runtime"
)

// validateServeAuth enforces the startup authentication invariants.
// --allow-anonymous grants every request admin rights (resolvePrincipal), so it
// must never be combined with existing accounts, and --prod requires the
// explicit LOOM_ALLOW_ANONYMOUS_UNSAFE=1 override.
func validateServeAuth(userCount int, token string, allowAnonymous, prod, allowAnonymousUnsafe bool) error {
	if allowAnonymous && userCount > 0 {
		return errors.New("--allow-anonymous grants unauthenticated admin access and cannot be used when users exist")
	}
	if allowAnonymous && prod && !allowAnonymousUnsafe {
		return errors.New("--allow-anonymous grants unauthenticated admin access and is refused with --prod; set LOOM_ALLOW_ANONYMOUS_UNSAFE=1 only for a deliberate throwaway production instance")
	}
	if userCount == 0 && strings.TrimSpace(token) == "" && !allowAnonymous {
		return errors.New("no users configured: run `loom user create --email you@example.com --role admin --password-stdin`, set LOOM_BOOTSTRAP_ADMIN_EMAIL and LOOM_BOOTSTRAP_ADMIN_PASSWORD once, or pass --allow-anonymous for local development")
	}
	return nil
}

// validateSubprocessTokenGate enforces the subprocess/service-token boundary
// (finding F1's residual): with a service token configured, code.python runs
// as the service user and can read the data directory and keyring on hosts
// without an OS sandbox, making an editor effectively an admin. The operator
// must isolate the subprocess-capable instance from the token, or explicitly
// acknowledge the merged privilege boundary.
func validateSubprocessTokenGate(enableSubprocess bool, token, ackEnv string) error {
	if !enableSubprocess || strings.TrimSpace(token) == "" {
		return nil
	}
	if strings.TrimSpace(ackEnv) == "1" {
		return nil
	}
	return errors.New("FATAL: --enable-subprocess-nodes combined with a service token (--token/LOOM_TOKEN) breaks the editor/admin boundary: Python runs as the service user and can read service data and keys. Run subprocess-capable instances in an isolated container without the token, or set LOOM_SUBPROCESS_UNSAFE_WITH_TOKEN=1 to acknowledge that editors are effectively admins in this deployment")
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":5678", "listen address")
	data := fs.String("data", "./data", "data directory")
	token := fs.String("token", os.Getenv("LOOM_TOKEN"), "bearer token required for API requests")
	prod := fs.Bool("prod", os.Getenv("LOOM_PROD") == "true" || os.Getenv("LOOM_PROD") == "1", "enforce strict enterprise production mode")
	retentionDays := fs.Int("retention-days", 30, "prune terminal runs older than N days (0 = off)")
	sim := fs.Bool("sim", false, "use the deterministic simulated host")
	recordEffects := fs.Bool("record-effects", true, "record external effects so runs replay hermetically without network access")
	allowAnonymous := fs.Bool("allow-anonymous", os.Getenv("LOOM_ALLOW_ANONYMOUS") == "1", "development only: allow unauthenticated admin access (production additionally requires LOOM_ALLOW_ANONYMOUS_UNSAFE=1)")
	allowAnonymousUnsafe := os.Getenv("LOOM_ALLOW_ANONYMOUS_UNSAFE") == "1"
	allowPrivateTargets := fs.Bool("allow-private-targets", os.Getenv("LOOM_ALLOW_PRIVATE_TARGETS") == "1", "self-hosted: allow live requests to private and loopback addresses (disables the SSRF private-target guard)")
	httpProxy := fs.String("http-proxy", os.Getenv("LOOM_HTTP_PROXY"), "route live HTTP requests through this proxy (trusted operator config; the SSRF target guard applies to the proxy address only)")
	enableSubprocess := fs.Bool("enable-subprocess-nodes", os.Getenv("LOOM_ENABLE_SUBPROCESS_NODES") == "1", "allow code.python subprocess execution and build its dependency environments at publish time (the largest security surface in Loom — opt in deliberately)")
	subprocessNetwork := fs.String("subprocess-network", func() string {
		if v := strings.TrimSpace(os.Getenv("LOOM_SUBPROCESS_NETWORK")); v != "" {
			return v
		}
		return "allow"
	}(), "code.python network posture: allow (scripts may call out) or deny (the OS cuts python's network; execution fails closed when the mechanism is unavailable)")
	subprocessPolicy := fs.String("subprocess-policy", "", "path to the subprocess package policy JSON (default <data>/subprocess-policy.json; a missing default file allows all pinned packages with a 1 GiB-per-env cap)")
	egressPolicy := fs.String("egress-policy", "", "path to the egress policy JSON (default <data>/egress-policy.json; a missing default file allows all destinations; mode \"deny\" restricts http.request, OAuth token URLs, proxies and db.postgres to the allowlist)")
	cookieSecure := fs.Bool("cookie-secure", os.Getenv("LOOM_COOKIE_SECURE") == "1", "set the Secure flag on session cookies (enable behind HTTPS)")
	trustForwardedHost := fs.Bool("trust-forwarded-host", os.Getenv("LOOM_TRUST_FORWARDED_HOST") == "1", "trust X-Forwarded-Host when checking login Origin (enable only behind a proxy that overwrites the header)")
	trustedProxyCIDR := fs.String("trusted-proxy-cidr", os.Getenv("LOOM_TRUSTED_PROXY_CIDRS"), "comma-separated proxy CIDRs whose X-Forwarded-For hop is trusted for login throttling and auth audit events")
	rejectWebhookQuerySecrets := fs.Bool("reject-webhook-query-secrets", os.Getenv("LOOM_REJECT_WEBHOOK_QUERY_SECRETS") == "1", "reject webhook authentication via ?secret= outright instead of accepting it with a deprecation warning (header and bearer authentication are unaffected)")
	sessionTTL := fs.Duration("session-ttl", 12*time.Hour, "session lifetime")
	// The read pool serves steady-state queries; the writer stays at one
	// connection. Serve validates the env strictly: a long-running process
	// must not silently run on a garbage configuration.
	dbReadersDefault := store.DefaultReadPoolSize
	if raw := strings.TrimSpace(os.Getenv("LOOM_DB_READERS")); raw != "" {
		parsed, perr := strconv.Atoi(raw)
		if perr != nil || parsed < 1 {
			return fmt.Errorf("invalid LOOM_DB_READERS %q: must be an integer >= 1", raw)
		}
		dbReadersDefault = parsed
	}
	dbReadersFlag := fs.Int("db-readers", dbReadersDefault, "read-only database connection pool size (minimum 1; env LOOM_DB_READERS)")
	// The sweep is the fallback wake mechanism, not the primary one: exact-deadline
	// scheduling resumes waits at their persisted wake time, and the sweep only
	// catches anything the timer path missed.
	waitSweepDefault := 15 * time.Second
	if raw := strings.TrimSpace(os.Getenv("LOOM_WAIT_SWEEP_INTERVAL")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("invalid LOOM_WAIT_SWEEP_INTERVAL %q: %w", raw, err)
		}
		waitSweepDefault = parsed
	}
	waitSweepInterval := fs.Duration("wait-sweep-interval", waitSweepDefault, "fallback sweep interval for durable wait wakeups (100ms minimum; exact-deadline scheduling is the primary wake mechanism)")
	maxTokenLifetimeDefault := 720 * time.Hour
	if raw := strings.TrimSpace(os.Getenv("LOOM_MAX_TOKEN_LIFETIME")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("invalid LOOM_MAX_TOKEN_LIFETIME %q: %w", raw, err)
		}
		maxTokenLifetimeDefault = parsed
	}
	maxTokenLifetime := fs.Duration("max-token-lifetime", maxTokenLifetimeDefault, "maximum API-token lifetime: a token created without an expiry gets exactly this, longer expiries are rejected (0 disables the cap and allows non-expiring tokens)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *waitSweepInterval < 100*time.Millisecond {
		return fmt.Errorf("--wait-sweep-interval must be at least 100ms, got %s", *waitSweepInterval)
	}

	if *maxTokenLifetime < 0 {
		return fmt.Errorf("--max-token-lifetime must not be negative, got %s", *maxTokenLifetime)
	}

	if *dbReadersFlag < 1 {
		return fmt.Errorf("--db-readers must be at least 1, got %d", *dbReadersFlag)
	}

	if *prod && strings.TrimSpace(*token) != "" && len(strings.TrimSpace(*token)) < 16 {
		return errors.New("FATAL: --prod mode requires a strong bearer token (minimum 16 characters) when one is set")
	}

	if err := validateSubprocessTokenGate(*enableSubprocess, *token, os.Getenv("LOOM_SUBPROCESS_UNSAFE_WITH_TOKEN")); err != nil {
		return err
	}

	// Egress policy (D7-6): mirrors the subprocess-policy precedent —
	// explicit path or the default <data>/egress-policy.json; a missing
	// default file means allow-all. An explicit-but-bad file fails fast
	// instead of first use.
	egressPol, err := egress.LoadPolicyFile(strings.TrimSpace(*egressPolicy), *data)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	subprocessNetworkMode := strings.TrimSpace(*subprocessNetwork)
	if subprocessNetworkMode == "" {
		subprocessNetworkMode = "allow"
	}
	if subprocessNetworkMode != "allow" && subprocessNetworkMode != "deny" {
		return fmt.Errorf("--subprocess-network must be allow or deny")
	}
	st, eng, err := openRuntimeOpts(runtimeOptions{
		data: *data, live: !*sim, allowPrivate: *allowPrivateTargets,
		proxy: strings.TrimSpace(*httpProxy), subprocess: *enableSubprocess,
		readers: *dbReadersFlag, egress: egressPol,
		subprocessNet: subprocessNetworkMode,
	})
	if err != nil {
		return err
	}
	defer st.Close()
	// Egress state for /healthz: report only what the live host actually
	// enforces (capability-derived, per the subprocessNodes precedent) —
	// a -sim serve has no outbound traffic, so it reports nothing.
	var egressState server.EgressState
	if blob, ok := eng.Host.(*host.BlobHost); ok {
		if live, ok := blob.Host.(*host.Live); ok && live.Egress != nil {
			egressState = server.EgressState{
				Mode:      string(live.Egress.Mode),
				Allowlist: len(live.Egress.Allowlist),
			}
			if egressState.Mode == "" {
				egressState.Mode = string(egress.ModeAllow)
			}
		}
	}
	if closer, ok := eng.Host.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	if *recordEffects {
		eng.DefaultEffects = cassette.ModeRecord
	}
	eng.DurableWaits = true
	// Exact-deadline wait scheduling: the scheduler arms a timer for the
	// earliest persisted wakeup and resumes runs at their deadline; the
	// sweep interval is only crash recovery for anything the timer path
	// missed. Run's initial reconcile also covers restart recovery.
	waitSched := engine.NewWaitScheduler(eng.ResumeDue, st.NextWakeup, *waitSweepInterval)
	waitSched.OnResume = func(n int) { log.Printf("resumed %d durable wait(s)", n) }
	eng.OnRunParked = func(time.Time) { waitSched.Schedule() }
	go waitSched.Run(ctx)
	go func() {
		if n := eng.SweepApprovalReminders(ctx); n > 0 {
			log.Printf("sent %d approval reminder(s)", n)
		}
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if n := eng.SweepApprovalReminders(ctx); n > 0 {
					log.Printf("sent %d approval reminder(s)", n)
				}
			}
		}
	}()

	if count, err := st.CountUsers(); err != nil {
		return err
	} else if count == 0 {
		email := strings.TrimSpace(os.Getenv("LOOM_BOOTSTRAP_ADMIN_EMAIL"))
		password := os.Getenv("LOOM_BOOTSTRAP_ADMIN_PASSWORD")
		if email != "" && password != "" {
			hash, err := auth.HashPassword(password)
			if err != nil {
				return fmt.Errorf("bootstrap admin: %w", err)
			}
			if _, err := st.CreateUser(store.User{
				Email: email, Name: "Admin", Role: "admin", PasswordHash: hash,
			}); err != nil {
				return fmt.Errorf("bootstrap admin: %w", err)
			}
			log.Printf("created bootstrap admin %s; unset LOOM_BOOTSTRAP_ADMIN_PASSWORD", email)
		}
	}
	userCount, err := st.CountUsers()
	if err != nil {
		return err
	}
	if w := bootstrapCredWarning(userCount, os.Getenv("LOOM_BOOTSTRAP_ADMIN_PASSWORD") != ""); w != "" {
		log.Print(w)
	}
	if err := validateServeAuth(userCount, *token, *allowAnonymous, *prod, allowAnonymousUnsafe); err != nil {
		return err
	}

	hostname, _ := os.Hostname()
	owner := fmt.Sprintf("%s/%d", hostname, os.Getpid())
	const leaseTTL = 5 * time.Second

	var schedMu sync.Mutex
	var scheduler *server.Scheduler
	var cancelSched context.CancelFunc

	startScheduler := func() error {
		schedMu.Lock()
		defer schedMu.Unlock()
		if scheduler != nil {
			return nil
		}
		var sCtx context.Context
		sCtx, cancelSched = context.WithCancel(ctx)
		if stale, err := st.StaleRuns(); err != nil {
			log.Printf("stale run scan: %v", err)
		} else {
			for _, run := range stale {
				run := run
				log.Printf("resuming interrupted run %s (%s)", run.ID, run.Status)
				go func() {
					if _, err := eng.ResumeInterrupted(sCtx, run.ID); err != nil {
						log.Printf("resume interrupted run %s: %v", run.ID, err)
					}
				}()
			}
		}
		// Parked (queued) runs start once interrupted runs have claimed
		// their slots; the delay lets the sweep flip them to running first.
		go func() {
			time.Sleep(2 * time.Second)
			eng.DrainQueued(sCtx)
		}()
		if *retentionDays > 0 {
			prune := func() {
				result, err := st.Prune(time.Now().AddDate(0, 0, -*retentionDays))
				if err != nil {
					log.Printf("retention prune: %v", err)
					return
				}
				if result.Runs > 0 || result.Blobs > 0 {
					log.Printf("retention pruned %d runs, %d events, %d blobs (~%s)",
						result.Runs, result.Events, result.Blobs, formatBytes(result.Bytes))
				}
			}
			prune()
			go func() {
				ticker := time.NewTicker(24 * time.Hour)
				defer ticker.Stop()
				for {
					select {
					case <-sCtx.Done():
						return
					case <-ticker.C:
						prune()
					}
				}
			}()
		}

		if n, err := server.MigrateCronWorkflows(st); err != nil {
			log.Printf("cron migration: %v", err)
		} else if n > 0 {
			log.Printf("migrated %d workflow cron trigger(s) into jobs", n)
		}

		scheduler = server.NewScheduler(sCtx, st, eng)
		if err := scheduler.Reload(); err != nil {
			return err
		}
		scheduler.Start()
		log.Printf("scheduler started (active instance: %s)", owner)
		return nil
	}

	stopScheduler := func() {
		schedMu.Lock()
		defer schedMu.Unlock()
		if scheduler != nil {
			scheduler.Stop()
			scheduler = nil
		}
		if cancelSched != nil {
			cancelSched()
			cancelSched = nil
		}
	}
	defer func() {
		stopScheduler()
		_ = st.ReleaseLease("scheduler", owner)
		eng.CancelAll()
	}()

	owned, err := st.AcquireLease("scheduler", owner, leaseTTL)
	if err != nil {
		return err
	}
	if owned {
		if err := startScheduler(); err != nil {
			return err
		}
	} else {
		log.Printf("scheduler lease held by another instance; standby will monitor for failover")
	}

	go func() {
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()
		isLeader := owned
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ok, err := st.AcquireLease("scheduler", owner, leaseTTL)
				if err != nil {
					log.Printf("scheduler lease check: %v", err)
					continue
				}
				if ok && !isLeader {
					if err := startScheduler(); err == nil {
						isLeader = true
					}
				} else if !ok && isLeader {
					log.Printf("scheduler lease lost; reverting to standby")
					stopScheduler()
					isLeader = false
				}
			}
		}
	}()

	reload := func() error {
		schedMu.Lock()
		defer schedMu.Unlock()
		if scheduler != nil {
			return scheduler.Reload()
		}
		return nil
	}

	e2eCSRF := ""
	if *sim {
		e2eCSRF = os.Getenv("LOOM_E2E_CSRF")
		if e2eCSRF != "" {
			log.Printf("WARNING: LOOM_E2E_CSRF is set; CSRF protection is relaxed for tests")
		}
	}

	switch {
	case *allowAnonymous:
		// The exact notice is printed by startupPostureNotice below.
	case userCount > 0:
		fmt.Printf("authentication enabled (%d user(s))\n", userCount)
	case *token != "":
		fmt.Println("API authentication enabled (Bearer token)")
	}
	listen := *addr
	if strings.HasPrefix(listen, ":") {
		listen = "localhost" + listen
	}
	fmt.Printf("loom %s listening on http://%s\n", version, listen)
	var buildEnvs func([]string) error
	var listPythonEnvs func() ([]pyenv.EnvInfo, error)
	var subprocessState server.SubprocessState
	if *enableSubprocess {
		envsRoot := filepath.Join(*data, "envs")
		policyPath := strings.TrimSpace(*subprocessPolicy)
		explicit := policyPath != ""
		if policyPath == "" {
			policyPath = filepath.Join(*data, "subprocess-policy.json")
		}
		// Fail fast on an unreadable/invalid policy instead of first use,
		// and capture the effective cap for /healthz.
		startupPolicy, err := pyenv.LoadPolicy(policyPath, explicit)
		if err != nil {
			return err
		}
		buildEnvs = func(requirements []string) error {
			policy, err := pyenv.LoadPolicy(policyPath, explicit)
			if err != nil {
				return err
			}
			if err := policy.Check(requirements); err != nil {
				return err
			}
			key, err := pyenv.Build(envsRoot, requirements)
			if err != nil {
				return err
			}
			if err := policy.CheckSize(envsRoot, key); err != nil {
				_ = os.RemoveAll(filepath.Join(envsRoot, key))
				return err
			}
			return nil
		}
		osLevel := "sandbox-exec"
		containerNote := ""
		if runtime.GOOS == "linux" {
			// Linux confines code.python's network when --subprocess-network
			// deny is set (an empty network namespace); the filesystem
			// stays unconfined, so the container advice stands either way.
			osLevel = "none"
			containerNote = "run subprocess workflows in a container"
		}
		// healthz must advertise only what the engine host can actually
		// do: -sim builds a non-executor host, so the flag alone must not
		// read as enabled (the frontend gates its Python preflight on
		// this signal).
		_, hostExecutes := eng.Host.(flow.Executor)
		subprocessState = server.SubprocessState{
			Enabled:   hostExecutes,
			Policy:    policyPath,
			CapMB:     int(startupPolicy.MaxEnvMB),
			OSLevel:   osLevel,
			Container: containerNote,
			Network:   subprocessNetworkMode,
		}
		if hostExecutes {
			listPythonEnvs = func() ([]pyenv.EnvInfo, error) {
				return pyenv.List(envsRoot), nil
			}
		}
	}
	if notice := startupPostureNotice(*allowAnonymous, *prod, allowAnonymousUnsafe, subprocessState); notice != "" {
		fmt.Fprint(os.Stderr, notice)
	}
	capabilities := capabilityState(*prod, *allowAnonymous, allowAnonymousUnsafe, strings.TrimSpace(*token) != "", subprocessState, egressState)
	auditStartupCapabilities(st, capabilities)
	trustedProxies, err := parseTrustedProxyCIDRs(*trustedProxyCIDR)
	if err != nil {
		return err
	}
	// Profile connection tests ride the live host's guarded HTTP and
	// postgres machinery; the simulated host has none (mirrors the executor
	// check above — interface embedding only promotes flow.Host's method
	// set, so the BlobHost wrapper is unwrapped explicitly).
	var profileTester server.ProfileTester
	if blobHost, ok := eng.Host.(*host.BlobHost); ok {
		if liveTester, ok := blobHost.Host.(*host.Live); ok {
			profileTester = liveTester
		}
	}
	// Python script tests ride the live host's subprocess executor; the
	// simulated host never implements it (Phase 4). JS script tests run
	// in-process on every server.
	var pythonExecutor flow.Executor
	if blobHost, ok := eng.Host.(*host.BlobHost); ok {
		if live, ok := blobHost.Host.(*host.Live); ok {
			pythonExecutor = live
		}
	}
	return server.Run(ctx, *addr, server.Deps{
		Version:                   version,
		Store:                     st,
		Engine:                    eng,
		Token:                     *token,
		Reload:                    reload,
		AllowAnonymous:            *allowAnonymous,
		CookieSecure:              *cookieSecure,
		Prod:                      *prod,
		TrustForwardedHost:        *trustForwardedHost,
		TrustedProxyCIDRs:         trustedProxies,
		RejectWebhookQuerySecrets: *rejectWebhookQuerySecrets,
		MaxTokenLifetime:          *maxTokenLifetime,
		BuildEnvs:                 buildEnvs,
		ListPythonEnvs:            listPythonEnvs,
		TestProfile:               profileTester,
		PythonExecutor:            pythonExecutor,
		SubprocessState:           subprocessState,
		EgressState:               egressState,
		Capabilities:              capabilities,
		SessionTTL:                *sessionTTL,
		CSRFOverride:              e2eCSRF,
	})
}

// bootstrapCredWarning returns the startup hygiene warning for an exported
// LOOM_BOOTSTRAP_ADMIN_PASSWORD that can no longer have any effect because
// users already exist.
func bootstrapCredWarning(userCount int, passwordSet bool) string {
	if userCount <= 0 || !passwordSet {
		return ""
	}
	return fmt.Sprintf("WARNING: LOOM_BOOTSTRAP_ADMIN_PASSWORD is still set but %d users exist; the bootstrap credentials are unused — unset the variable", userCount)
}

// parseTrustedProxyCIDRs parses the comma-separated --trusted-proxy-cidr value.
// Only a network whose reverse proxy is known to overwrite X-Forwarded-For
// belongs here; an untrusted network would let clients choose throttle keys.
func parseTrustedProxyCIDRs(raw string) ([]*net.IPNet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []*net.IPNet
	for index, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("trusted proxy CIDR %d is empty", index+1)
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q: must use CIDR notation such as 10.0.0.0/8", value)
		}
		out = append(out, network)
	}
	return out, nil
}
