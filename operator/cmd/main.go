package main

import (
	"errors"
	"flag"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
	"github.com/maci0/baseline-security-operator/internal/controller"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(baselinev1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr, probeAddr, metricsCertDir string
	var enableLeaderElection, secureMetrics bool
	// HTTPS + authn/authz (TokenReview / SubjectAccessReview), matching
	// kubebuilder / Operator SDK defaults and OpenShift CONVENTIONS.md.
	// Disable the endpoint with --metrics-bind-address=0.
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8443", "Metrics endpoint address. Use 0 to disable.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true, "Serve metrics over HTTPS with authentication and authorization.")
	flag.StringVar(&metricsCertDir, "metrics-cert-dir", "/var/run/metrics-certs", "Directory with tls.crt/tls.key for metrics (service-ca). Empty or missing files fall back to self-signed.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Health probe endpoint address.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true, "Enable leader election.")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := ctrl.Log.WithName("setup")

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
		os.Exit(1)
	}

	if !secureMetrics && metricsAddr != "0" && !isLoopbackMetricsAddr(metricsAddr) {
		setupLog.Info("refusing non-loopback insecure metrics; forcing metrics-secure=true",
			"metricsBindAddress", metricsAddr)
		secureMetrics = true
	}

	// Non-secret config only. RELATED_IMAGE value is logged as set/unset so a
	// misdeployed pod is obvious without printing the image pull path.
	relatedImage := strings.TrimSpace(os.Getenv("RELATED_IMAGE_CONSOLE_PLUGIN"))
	relatedImageSet := relatedImage != ""
	// Log only validity so registry paths never hit stdout.
	relatedImageValid := relatedImageSet && controller.ValidRelatedImage(relatedImage)
	skipDefaultCR := envTruthy("BASELINE_SECURITY_SKIP_DEFAULT_CR")
	setupLog.Info("configuration",
		"metricsBindAddress", metricsAddr,
		"metricsSecure", secureMetrics,
		"metricsCertDir", metricsCertDir,
		"healthProbeBindAddress", probeAddr,
		"leaderElect", enableLeaderElection,
		"relatedImageConsolePluginSet", relatedImageSet,
		"relatedImageConsolePluginValid", relatedImageValid,
		"skipDefaultClusterBaseline", skipDefaultCR,
	)
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
	utilruntime.Must(mgr.AddReadyzCheck("readyz", healthz.Ping))

	// Zero-config default: create ClusterBaseline/cluster if none exists.
	// Opt out with BASELINE_SECURITY_SKIP_DEFAULT_CR=true. Leader-only so
	// HA replicas do not race the create on every pod.
	if !skipDefaultCR {
		utilruntime.Must(mgr.Add(&defaultClusterBaseline{
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
}

// errEmptyHealthProbeAddr is logged when --health-probe-bind-address is empty.
var errEmptyHealthProbeAddr = errors.New("empty health-probe-bind-address")

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

// envTruthy is true for common affirmative env values (true/1/yes), after
// trim and case-fold. Empty, "false", "0", and junk are false.
func envTruthy(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}
