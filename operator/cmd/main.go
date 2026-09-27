// Operator process entrypoint (kubebuilder go/v4 layout).
//
// Files in this package:
//   - main.go: flag parse, scheme, manager, health probes, controller SetupWithManager
//   - metrics_cert.go: service-ca / self-signed TLS cert provider for secure metrics
//
// The leader-elected create of ClusterBaseline/cluster when none exist is a
// manager Runnable, so it lives beside the reconciler in
// internal/controller (default_cr.go), not here.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
	"github.com/maci0/baseline-security-operator/internal/controller"
)

// envSkipDefaultCR opts out of creating ClusterBaseline/cluster when none exist.
// Keep the string in one place so README/CSV comments and code cannot drift.
const envSkipDefaultCR = "BASELINE_SECURITY_SKIP_DEFAULT_CR"

// errShuttingDown fails the readiness check once SIGTERM has been received.
var errShuttingDown = errors.New("shutting down")

// errCacheNotSynced fails the readiness check while the informers are still
// catching up. Local to the readyz check; the default-CR runnable in
// internal/controller keeps its own sentinel.
var errCacheNotSynced = errors.New("cache did not sync")

// shuttingDown is set by a manager runnable when the signal handler cancels the
// manager context, so readyz reports 503 while the process drains.
var shuttingDown atomic.Bool

// shutdownFlag sets shuttingDown the moment the manager context is cancelled.
// It must run on every replica, not only the leader: a bare manager.RunnableFunc
// does not implement LeaderElectionRunnable, so controller-runtime files it in
// the leader-election runnable group and a standby replica never starts it.
// The Deployment ships 2 replicas, so a terminating standby would keep reporting
// Ready through the whole drain and stay in Service endpoints.
type shutdownFlag struct{}

func (*shutdownFlag) NeedLeaderElection() bool { return false }

func (*shutdownFlag) Start(ctx context.Context) error {
	<-ctx.Done()
	// Without this the last line a healthy pod ever emits is "starting manager":
	// a clean rollout and a crash-looping pod produce the same log, and a drain
	// cut short by the 20s gracefulShutdownTimeout (against a 30s pod grace)
	// leaves no record that it happened.
	setupLog.Info("shutting down; draining manager", "timeout", gracefulShutdownTimeout)
	shuttingDown.Store(true)
	return nil
}

var scheme = runtime.NewScheme()

// setupLog is the process-lifecycle logger. Package-level so the runnables
// below can reach it, not just main(). ctrl.Log is a delegating sink, so
// building this before SetLogger still forwards to the configured logger.
var setupLog = ctrl.Log.WithName("setup")

// version is stamped by the linker from the build ARG (operator/Makefile
// VERSION, the CSV version, and the OCI version label all read the same value).
// The default marks a binary built without it (plain `go build`, `go run`).
var version = "dev"

// gracefulShutdownTimeout is how long the manager may spend draining before the
// process exits anyway. It must stay well under the pod's 30s
// terminationGracePeriodSeconds, or the kubelet SIGKILLs the process mid-drain
// instead of letting the leader lease release cleanly.
const gracefulShutdownTimeout = 20 * time.Second

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(baselinev1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr, probeAddr, metricsCertDir string
	var enableLeaderElection, secureMetrics, showVersion bool
	// HTTPS + authn/authz (TokenReview / SubjectAccessReview), matching
	// kubebuilder / Operator SDK defaults and OpenShift CONVENTIONS.md.
	// Disable the endpoint with --metrics-bind-address=0.
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8443", "Metrics endpoint address. Use 0 to disable.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true, "Serve metrics over HTTPS with authentication and authorization.")
	flag.StringVar(&metricsCertDir, "metrics-cert-dir", "/var/run/metrics-certs", "Directory with tls.crt/tls.key for metrics (service-ca). Empty or missing files fall back to self-signed.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Health probe endpoint address.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true, "Enable leader election.")
	flag.BoolVar(&showVersion, "version", false, "Print the version and exit.")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	// clientconfig registers --kubeconfig on the default FlagSet from a package
	// init, so the flag a reconcile depends on would exist only because a
	// transitive package has a side effect. Register it here instead: the
	// process flag surface is then the list above plus this one. ctrl.GetConfigOrDie
	// resolves it in the order --kubeconfig, KUBECONFIG, in-cluster, $HOME/.kube/config.
	clientconfig.RegisterFlags(flag.CommandLine)
	// --help must be pipeable (`manager --help | less`), so the help text goes
	// to stdout. Everything that reports a bad invocation (unknown flag,
	// unexpected argument) is an error: it goes to stderr with the usage text,
	// so a script that captures stdout on the exit-2 path sees no output.
	if err := parseArgs(os.Args[1:], os.Stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		usageError(err)
	}

	// --version is data for a script or a support bundle, so it goes to stdout
	// and exits 0 before any cluster, config, or port work.
	if showVersion {
		fmt.Printf("%s %s\n", filepath.Base(os.Args[0]), version)
		os.Exit(0)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Normalize flag strings so padding from shell/YAML does not change bind
	// semantics or bypass loopback checks (e.g. " 0 " vs "0").
	metricsAddr = strings.TrimSpace(metricsAddr)
	probeAddr = strings.TrimSpace(probeAddr)
	metricsCertDir = strings.TrimSpace(metricsCertDir)

	// Empty BindAddress is not "disabled": controller-runtime maps it to
	// ":8080" (all interfaces). Restore the flag default instead.
	if metricsAddr == "" {
		setupLog.Info("metrics-bind-address empty; using :8443")
		metricsAddr = ":8443"
	}
	// Empty probe address disables the manager endpoints while the Deployment
	// still probes :8081, so the pod never becomes Ready. Fail fast.
	if probeAddr == "" {
		setupLog.Error(errEmptyHealthProbeAddr, "health-probe-bind-address must not be empty (Deployment probes :8081)")
		os.Exit(2)
	}
	// Reject host:port typos before manager start so a bad Deployment arg is
	// obvious in setup logs instead of a later bind failure.
	if err := validateListenAddr(metricsAddr, true); err != nil {
		setupLog.Error(err, "invalid metrics-bind-address (want host:port, or 0 to disable)",
			"metricsBindAddress", metricsAddr)
		os.Exit(2)
	}
	if err := validateListenAddr(probeAddr, false); err != nil {
		setupLog.Error(err, "invalid health-probe-bind-address (want host:port)",
			"healthProbeBindAddress", probeAddr)
		os.Exit(2)
	}
	// Relative cert dirs depend on process CWD and break under a read-only
	// rootfs / different workdir. Require absolute when set.
	if err := validateMetricsCertDir(metricsCertDir); err != nil {
		setupLog.Error(err, "invalid metrics-cert-dir (want absolute path, or empty for self-signed only)",
			"metricsCertDir", metricsCertDir)
		os.Exit(2)
	}

	if !secureMetrics && metricsAddr != "0" && !isLoopbackMetricsAddr(metricsAddr) {
		setupLog.Info("refusing non-loopback insecure metrics; forcing metrics-secure=true",
			"metricsBindAddress", metricsAddr)
		secureMetrics = true
	}

	// Non-secret config only. RELATED_IMAGE value is logged as set/unset so a
	// misdeployed pod is obvious without printing the image pull path.
	relatedImage := strings.TrimSpace(os.Getenv(controller.EnvRelatedImageConsolePlugin))
	relatedImageSet := relatedImage != ""
	// Log only validity so registry paths never hit stdout.
	relatedImageValid := relatedImageSet && controller.ValidRelatedImage(relatedImage)
	skipDefaultCR, err := parseEnvBool(envSkipDefaultCR)
	if err != nil {
		setupLog.Error(err, "invalid "+envSkipDefaultCR+" (unset creates ClusterBaseline/cluster)")
		os.Exit(1)
	}
	setupLog.Info("configuration",
		"metricsBindAddress", metricsAddr,
		"metricsSecure", secureMetrics,
		"metricsCertDir", metricsCertDir,
		"healthProbeBindAddress", probeAddr,
		"leaderElect", enableLeaderElection,
		"zapDevelopment", opts.Development,
		"zapEncoder", lookupFlag("zap-encoder"),
		"zapLogLevel", lookupFlag("zap-log-level"),
		"zapStacktraceLevel", lookupFlag("zap-stacktrace-level"),
		"relatedImageConsolePluginSet", relatedImageSet,
		"relatedImageConsolePluginValid", relatedImageValid,
		"skipDefaultClusterBaseline", skipDefaultCR,
		// Set/unset only: the path names a developer's home directory, which the
		// operator has no reason to print into a log shipped in must-gather.
		"kubeconfigFlagSet", lookupFlag(clientconfig.KubeconfigFlagName) != "",
	)
	if !enableLeaderElection {
		// Deployment ships 2 replicas; without a lease both leaders reconcile.
		setupLog.Info("leader election disabled; multi-replica Deployments may race on reconcile and default CR create")
	}
	if opts.Development {
		// Development mode is a zap flag for local debugging; warn so a
		// mis-set CSV/Deployment arg is obvious in production pod logs.
		setupLog.Info("zap development logging enabled (--zap-devel); not recommended for production")
	}
	// Empty cert dir with secure metrics always falls back to self-signed;
	// OpenShift service-ca scrapers will fail until the path is set.
	if secureMetrics && metricsAddr != "0" && metricsCertDir == "" {
		setupLog.Info("metrics-cert-dir empty; secure metrics will use a self-signed cert (service-ca scrapers will not trust it)")
	}
	if !relatedImageSet {
		setupLog.Info("RELATED_IMAGE_CONSOLE_PLUGIN is unset; console plugin stays ImageMissing until the env is fixed")
	} else if !relatedImageValid {
		setupLog.Info("RELATED_IMAGE_CONSOLE_PLUGIN is set but not a valid image reference; console plugin stays ImageInvalid until the env is fixed")
	}

	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
	}
	if secureMetrics {
		// Dynamic GetCertificate reloads service-ca files when they appear after
		// startup (optional volume); falls back to self-signed until then.
		metricsServerOptions.TLSOpts = metricsTLSOpts(metricsCertDir)
		// Requires ClusterRole rules for tokenreviews and subjectaccessreviews.
		// Scrapers need nonResourceURLs: ["/metrics"] verbs: ["get"].
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "baseline-security-operator-lock",
		// Release the lease on graceful shutdown so a standby takes over immediately
		// instead of waiting out the ~15s lease on every rollout/drain. Safe because
		// the process exits right after mgr.Start returns (no post-Start work that
		// could race a new leader).
		LeaderElectionReleaseOnCancel: true,
		// Finish (or time out) draining well before the pod's 30s
		// terminationGracePeriodSeconds so the manager exits cleanly rather than
		// being SIGKILLed mid-shutdown. Reconciles are single atomic API calls that
		// fail fast once the context is cancelled, so 20s is ample.
		GracefulShutdownTimeout: ptr.To(gracefulShutdownTimeout),
		// Scope the informer cache to the namespaces the reconciler reads.
		// ClusterBaseline is the only cluster-scoped cached type; the rest
		// (plugin Deployment/Service/PDB, scan-storage PVCs) are namespaced, and
		// an unscoped cache would hold every one of those objects in the cluster
		// for the life of the process.
		Cache: controller.ManagerCacheOptions(),
		// Read ConfigMaps uncached (direct API). The operator only touches the one
		// named console dashboard ConfigMap in openshift-config-managed and holds
		// only named get/update on it, not the cluster-wide list/watch a cache
		// informer would require. Without this, the ConfigMap informer can never
		// sync and the reconcile blocks forever on the dashboard read.
		Client: client.Options{
			Cache: &client.CacheOptions{
				// Named dashboard CM only, held with named get/update (no cluster-wide
				// list/watch a cache informer needs). Unstructured reads (Infrastructure,
				// consoles, subscriptions, ...) already bypass the cache by default, so
				// they need no entry here.
				DisableFor: []client.Object{&corev1.ConfigMap{}},
			},
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := (&controller.ClusterBaselineReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterBaseline")
		os.Exit(1)
	}

	utilruntime.Must(mgr.AddHealthzCheck("healthz", healthz.Ping))
	utilruntime.Must(mgr.AddReadyzCheck("cache-sync", cacheSyncReadyz(mgr.GetCache())))
	// Flip the flag as soon as the signal handler cancels the manager context,
	// before the graceful drain runs, so the first probe after SIGTERM is 503.
	utilruntime.Must(mgr.Add(&shutdownFlag{}))

	// Zero-config default: create ClusterBaseline/cluster if none exists.
	// Opt out with BASELINE_SECURITY_SKIP_DEFAULT_CR=true. Leader-only so
	// HA replicas do not race the create on every pod.
	if !skipDefaultCR {
		utilruntime.Must(mgr.Add(&controller.DefaultClusterBaseline{
			Client: mgr.GetClient(),
			Cache:  mgr.GetCache(),
			Log:    setupLog,
		}))
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
	// Closes the process bracket started above. A pod that ends here drained and
	// exited 0; a pod that is missing this line was killed.
	setupLog.Info("manager stopped", "version", version)
}

// errEmptyHealthProbeAddr is logged when --health-probe-bind-address is empty.
var errEmptyHealthProbeAddr = errors.New("empty health-probe-bind-address")

// errRelativeMetricsCertDir is logged when --metrics-cert-dir is non-empty but
// not absolute (CWD-dependent paths are not supported in the container).
var errRelativeMetricsCertDir = errors.New("metrics-cert-dir must be an absolute path")

// validateMetricsCertDir allows empty (self-signed only) or an absolute path.
// Relative paths are rejected: the manager CWD is not a stable config surface.
func validateMetricsCertDir(dir string) error {
	if dir == "" {
		return nil
	}
	if !filepath.IsAbs(dir) {
		return errRelativeMetricsCertDir
	}
	return nil
}

// validateListenAddr requires host:port (port 1-65535). When disableOK, "0" is
// accepted (controller-runtime metrics disable). Empty is rejected: callers
// must normalize or fail-fast before this check.
func validateListenAddr(addr string, disableOK bool) error {
	if disableOK && addr == "0" {
		return nil
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("port %q is not a number", port)
	}
	if p < 1 || p > 65535 {
		return fmt.Errorf("port %q out of range 1-65535", port)
	}
	return nil
}

// isLoopbackMetricsAddr is true for disabled ("0") or explicit
// 127.0.0.1 / localhost binds. Empty is NOT safe: controller-runtime
// defaults an empty BindAddress to ":8080" (all interfaces).
func isLoopbackMetricsAddr(addr string) bool {
	if addr == "0" {
		return true
	}
	host := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	// "[::1]:8443" is loopback; ":8443" binds all interfaces (not loopback).
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// errInvalidEnvBool is returned by parseEnvBool for a set, non-empty value that
// is not a known true/false spelling. Callers must fail start: treating junk as
// false would silently create ClusterBaseline/cluster when an admin thought
// they had opted out.
var errInvalidEnvBool = errors.New("invalid boolean environment variable")

// envBoolValueMaxLog caps the rejected value in the error so a multi-kilobyte
// mis-set env cannot bloat setup logs.
const envBoolValueMaxLog = 64

// parseEnvBool reads key as a boolean. Unset, empty, and whitespace-only are
// false (the skip-default-CR opt-out is off). Known true/false spellings are
// accepted after trim and case-fold; anything else is an error so a typo
// cannot silently take the default.
func parseEnvBool(key string) (bool, error) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return false, nil
	}
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return false, nil
	}
	switch v {
	case "1", "true", "yes", "on", "y", "t", "enable", "enabled":
		return true, nil
	case "0", "false", "no", "off", "n", "f", "disable", "disabled":
		return false, nil
	default:
		shown := raw
		// Rune count, and a rune-boundary cut, not a byte slice: a value that
		// is mostly multibyte ("hééé…") hits the cap in far fewer than 64
		// bytes, and a byte cut can land mid-rune, putting a raw invalid byte
		// into the error string that %q then escapes as \x.., so the setup log
		// shows mojibake for a value the admin set in full.
		if n := utf8.RuneCountInString(shown); n > envBoolValueMaxLog {
			shown = string([]rune(shown)[:envBoolValueMaxLog]) + "..."
		}
		return false, fmt.Errorf("%w: %s=%q (want true/false, 1/0, yes/no, on/off, y/n, t/f, enable/disable)", errInvalidEnvBool, key, shown)
	}
}

// lookupFlag returns the string form of a parsed flag, or empty if missing.
// Used to log zap encoder/level overrides (empty means the zap default for
// --zap-devel true/false).
func lookupFlag(name string) string {
	f := flag.Lookup(name)
	if f == nil {
		return ""
	}
	return f.Value.String()
}

// unexpectedArgsError is non-nil when flag.Parse left positional arguments.
// The manager takes flags only; leftover args are almost always a boolean
// flag written as `--metrics-secure false` (space, so `false` is positional
// and the bool stays at its default), which the message names rather than
// leaving the caller to work out Go flag syntax.
func unexpectedArgsError(args []string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) == 1 {
		if _, err := strconv.ParseBool(args[0]); err == nil {
			return fmt.Errorf("unexpected argument %q: a boolean flag takes no separate value, write --flag=%s", args[0], args[0])
		}
	}
	return fmt.Errorf("unexpected arguments: %s", strings.Join(args, " "))
}

// parseArgs parses args into flag.CommandLine. It returns flag.ErrHelp once
// the help text has been written to w (an explicit --help is not an error, so
// it belongs on stdout), and a usage error for anything else, which the caller
// reports on stderr.
//
// ContinueOnError with a silent Usage is what keeps the two apart: under
// ExitOnError the flag package prints the usage to stdout on a parse error and
// prints the help text a second time on -h.
func parseArgs(args []string, w io.Writer) error {
	flag.Usage = func() {}
	flag.CommandLine.Init(filepath.Base(os.Args[0]), flag.ContinueOnError)
	// flag prints "flag provided but not defined" itself; discard it so usageError
	// can name the program the way the hack/ scripts do.
	flag.CommandLine.SetOutput(io.Discard)
	if err := flag.CommandLine.Parse(args); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			return err
		}
		if err := printUsage(w); err != nil {
			return err
		}
		return flag.ErrHelp
	}
	return unexpectedArgsError(flag.Args())
}

// usageError reports a bad invocation and exits 2 (usage error, not a runtime
// failure). Both the message and the usage text go to stderr: stdout carries
// only the pipeable --help output and log output never touches it.
func usageError(err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n\n", filepath.Base(os.Args[0]), err)
	if uerr := printUsage(os.Stderr); uerr != nil {
		fmt.Fprintln(os.Stderr, uerr)
	}
	os.Exit(2)
}

// printUsage writes --help to w (stdout for an explicit --help, stderr
// alongside a usage error). Env vars that affect the process are listed so
// --help matches the README process-configuration table.
func printUsage(w io.Writer) error {
	name := filepath.Base(os.Args[0])
	if _, err := fmt.Fprintf(w, "Usage: %s [flags]\n\n", name); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "ClusterBaseline operator process. Product config is ClusterBaseline/cluster.\n\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Flags:\n"); err != nil {
		return err
	}
	orig := flag.CommandLine.Output()
	flag.CommandLine.SetOutput(w)
	flag.PrintDefaults()
	flag.CommandLine.SetOutput(orig)
	if _, err := fmt.Fprintf(w, "\nEnvironment:\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  %s\n        If true, do not create ClusterBaseline/cluster when none exist.\n", envSkipDefaultCR); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  %s\n        Console plugin image to deploy. Unset leaves ImageMissing.\n", controller.EnvRelatedImageConsolePlugin); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "  KUBECONFIG\n        Out-of-cluster kubeconfig. Precedence: --kubeconfig, KUBECONFIG, in-cluster, $HOME/.kube/config.\n")
	return err
}

// cacheSyncReadyz is the readyz check: the pod serves only once the informers
// are in sync, and stops serving as soon as SIGTERM arrives. Ping alone would
// mark the pod ready while the caches are empty, so kubelet can route to a pod
// that cannot reconcile yet.
func cacheSyncReadyz(c cache.Cache) func(req *http.Request) error {
	return func(req *http.Request) error {
		// SIGTERM received: the process is draining, so fail readiness even
		// though the caches are still in sync. Endpoint removal normally
		// happens on SIGTERM anyway; this closes the window where a scrape or
		// a route still lands on a pod that is shutting down.
		if shuttingDown.Load() {
			return errShuttingDown
		}
		if !c.WaitForCacheSync(req.Context()) {
			return errCacheNotSynced
		}
		return nil
	}
}
