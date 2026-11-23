package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"loom/internal/egress"
	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/host"
	"loom/internal/secrets"
	"loom/internal/store"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var version = "0.1.0-dev"

var (
	errValidation = errors.New("validation failed")
	errRunFailed  = errors.New("run did not complete")
)

type jsonLogWriter struct{}

func (jsonLogWriter) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	record := map[string]any{
		"time":  time.Now().UTC().Format(time.RFC3339Nano),
		"level": "info",
		"msg":   msg,
	}
	data, _ := json.Marshal(record)
	data = append(data, '\n')
	return os.Stderr.Write(data)
}

func main() {
	if os.Getenv("LOOM_LOG_FORMAT") == "json" {
		log.SetFlags(0)
		log.SetOutput(jsonLogWriter{})
	}

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "validate":
		err = runValidate(os.Args[2:])
	case "plan":
		err = runPlan(os.Args[2:])
	case "serve":
		err = runServe(os.Args[2:])
	case "run":
		err = runFlow(os.Args[2:], false)
	case "test":
		err = runFlow(os.Args[2:], true)
	case "replay":
		err = runReplay(os.Args[2:])
	case "verify":
		err = runVerify(os.Args[2:])
	case "resume":
		err = runResume(os.Args[2:])
	case "export":
		err = runExport(os.Args[2:])
	case "import":
		err = runImport(os.Args[2:])
	case "workflows":
		err = runWorkflows(os.Args[2:])
	case "runs":
		err = runRuns(os.Args[2:])
	case "runbook":
		err = runRunbook(os.Args[2:])
	case "prune":
		err = runPrune(os.Args[2:])
	case "user":
		err = runUser(os.Args[2:])
	case "token":
		err = runToken(os.Args[2:])
	case "key":
		err = runKey(os.Args[2:])
	case "doctor":
		err = runDoctor(os.Args[2:])
	case "egress":
		err = runEgress(os.Args[2:])
	case "ops":
		err = runOps(os.Args[2:])
	case "backup":
		err = runBackup(os.Args[2:])
	case "ownership":
		err = runOwnership(os.Args[2:])
	case "profile":
		err = runProfile(os.Args[2:])
	case "schema":
		err = runSchema(os.Args[2:])
	case "script":
		err = runScript(os.Args[2:])
	case "connector":
		err = runConnector(os.Args[2:])
	case "mapping":
		err = runMapping(os.Args[2:])
	case "hub":
		err = runHub(os.Args[2:])
	case "version":
		fmt.Println("loom " + version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "loom: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	if err == nil {
		return
	}
	if errors.Is(err, errValidation) || errors.Is(err, errRunFailed) {
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "loom:", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprint(os.Stderr, `loom — typed, replayable workflows

usage:
  loom validate <file...>              validate one or more workflow files
  loom plan <file>                     print the execution DAG in parallel levels
  loom run [options] <file>            run a workflow (simulated by default)
  loom test [options] <file>           run with approvals auto-approved
  loom replay [options] <runID>        replay a recorded run
  loom verify --from-run <runID>       replay a workflow version against a recorded run (hermetic)
  loom resume [options] <runID>        resume a run waiting for approval
  loom export [options] <workflowID>   export a workflow as JSON
  loom import [options] <file>         import a workflow into the store
  loom workflows [options]             list stored workflows
  loom runs [options]                  list runs
  loom runbook [options] <file|id>     render a Markdown runbook
  loom prune [options]                 delete old runs and orphan blobs
  loom key <rotate|list> [options]     manage the signing-key keyring
  loom doctor [options]                verify every event chain and blob on disk
  loom backup [verify|restore] [opts]  snapshot, verify or restore the data directory
  loom ownership migrate [options]     plan, apply or roll back legacy profile ownership
  loom serve [options]                 start the API and embedded editor
  loom version                         print the version

run/test options:
  --data DIR        data directory (default ./data)
  --live            use the real network (default: simulated)
  --auto-approve    auto-approve approval nodes
  --input JSON      trigger input object (default {})
  --verbose         print every event
  --record-effects  record external effects so the run replays hermetically
  --enable-subprocess-nodes  allow code.python (env: LOOM_ENABLE_SUBPROCESS_NODES=1)

replay options:
  --data DIR        data directory (default ./data)
  --step NODE       verify one node against its recorded output (executes nothing)
  --execute         with --step: re-run that node (repeats side effects with --live)
  --from NODE       re-execute from a node onward as a new run
  --hermetic        with --step/--from: replay from recorded effects, no network
  --live            allow real network access during re-execution
  --enable-subprocess-nodes  allow code.python during live re-execution
  The recorded run is never modified; replays are recorded as their own runs.

verify options:
  --from-run RUNID  historical run to replay against (required)
  --version N       workflow version to verify (default: the run's snapshot)
  --data DIR        data directory (default ./data)

resume options:
  --data DIR        data directory (default ./data)
  --decision VALUE  approved (default) or rejected
  --note TEXT       decision note (required when rejecting)

prune options:
  --data DIR        data directory (default ./data)
  --before VALUE    cutoff: 30d, 24h or RFC3339 timestamp (default 30d)
  --dry-run         report what would be pruned, with a storage estimate,
                    without deleting anything

key options:
  --data DIR        data directory (default ./data)
  rotate            generate a new signing key; recorded runs keep verifying
  list              list keyring ids, marking the current and legacy keys

doctor options:
  --data DIR        data directory (default ./data)

backup options:
  --data DIR        data directory (default ./data)
  --out FILE        archive path (default loom-backup-<timestamp>.tar.gz)
  verify --backup F  extract the archive and verify all chains and blobs
  restore --backup F --to DIR  unpack an archive to a new data directory

ownership options:
  --data DIR        data directory (default ./data)
  --dry-run         write the migration report without changing drafts (default)
  --apply           stamp drafts with explicit profile ownership; requires --backup
  --backup FILE     verified loom backup archive created within 24h
  --rollback        restore the latest applied migration
  --report FILE     report path (default <data>/ownership-migration.json)

serve options:
  --addr ADDR       listen address (default :5678)
  --data DIR        data directory (default ./data)
  --token TOKEN     require Authorization: Bearer TOKEN (default $LOOM_TOKEN)
  --retention-days N  prune terminal runs older than N days on boot and daily (default 30; 0 = off)
  --sim             use the deterministic simulated host (tests and demos)
  --record-effects  record external effects for hermetic replay (default true; use --record-effects=false to disable)
  --prod            strict production mode: strong token, Secure cookies, cross-origin login rejected
  --allow-anonymous development only: allow unauthenticated admin access
  --allow-private-targets  self-hosted: allow live requests to private/loopback addresses (disables the SSRF target guard)
  --http-proxy URL  route live HTTP through a proxy (the target guard applies to the proxy address)
  --enable-subprocess-nodes  allow code.python and build its dependency environments at publish time
  --subprocess-policy FILE   package allowlist + per-env cap (default <data>/subprocess-policy.json)
  --cookie-secure   set the Secure flag on session cookies (enable behind HTTPS)
  --trust-forwarded-host     trust X-Forwarded-Host for login Origin checks (proxy only)
  --session-ttl D   session lifetime (default 12h)

docs: docs/quickstart.md · reference: docs/node-reference.md
`)
}

// runtimeOptions configures host construction for a CLI command.
type runtimeOptions struct {
	data          string
	live          bool
	allowPrivate  bool
	proxy         string
	subprocess    bool
	readers       int
	egress        *egress.Policy
	subprocessNet string // "allow" (default) or "deny" (isolation-runner D9-1)
}

func openRuntime(data string, live, allowPrivate bool) (*store.Store, *engine.Engine, error) {
	return openRuntimeOpts(runtimeOptions{data: data, live: live, allowPrivate: allowPrivate})
}

func openRuntimeProxy(data string, live, allowPrivate bool, proxyURL string) (*store.Store, *engine.Engine, error) {
	return openRuntimeOpts(runtimeOptions{data: data, live: live, allowPrivate: allowPrivate, proxy: proxyURL})
}

func openRuntimeOpts(opts runtimeOptions) (*store.Store, *engine.Engine, error) {
	st, err := openStoreWithReaders(opts.data, opts.readers)
	if err != nil {
		return nil, nil, err
	}

	resolveCred := func(id string) (flow.Credential, bool) {
		if cred, ok := secrets.FromEnv().Lookup(id); ok {
			return cred, true
		}
		if typ, raw, err := st.GetCredential(id); err != nil {
			var parsed map[string]any
			if json.Unmarshal(raw, &parsed) == nil {
				return flow.Credential{Type: typ, Data: parsed, Raw: string(raw)}, true
			}
			return flow.Credential{Type: typ, Raw: string(raw)}, true
		}
		return flow.Credential{}, false
	}

	var h flow.Host = host.NewSim()
	var liveHost *host.Live
	if opts.live {
		liveHost = host.NewLive(resolveCred)
		liveHost.AllowPrivate = opts.allowPrivate
		liveHost.Proxy = opts.proxy
		liveHost.EnableSubprocess = opts.subprocess
		liveHost.DenySubprocessNetwork = opts.subprocessNet == "deny"
		liveHost.Egress = opts.egress
		if opts.subprocess {
			liveHost.EnvsRoot = filepath.Join(opts.data, "envs")
		}
		h = host.NewBlobHost(liveHost, st)
	}
	eng := engine.New(st, h)
	eng.CredentialResolver = resolveCred
	return st, eng, nil
}

func openStore(data string) (*store.Store, error) {
	return openStoreWithReaders(data, dbReaders())
}

// dbReaders resolves the read pool size from LOOM_DB_READERS, falling back
// to the store's conservative default. Values below 1 use the default.
func dbReaders() int {
	raw := strings.TrimSpace(os.Getenv("LOOM_DB_READERS"))
	if raw == "" {
		return store.DefaultReadPoolSize
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return store.DefaultReadPoolSize
	}
	return n
}

func openStoreWithReaders(data string, readers int) (*store.Store, error) {
	st, err := store.OpenWithReadPool(data, readers)
	if err != nil {
		return nil, err
	}
	return st, nil
}
