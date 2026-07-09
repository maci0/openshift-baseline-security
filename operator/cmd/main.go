package main

import (
	"context"
	"flag"
	"os"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	baselinev1alpha1 "github.com/openshift-baseline-security/baseline-security-operator/api/v1alpha1"
	"github.com/openshift-baseline-security/baseline-security-operator/internal/controller"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(baselinev1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr, probeAddr string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8443", "Metrics endpoint address.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Health probe endpoint address.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true, "Enable leader election.")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := ctrl.Log.WithName("setup")

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr, SecureServing: true},
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
	// Opt out with BASELINE_SECURITY_SKIP_DEFAULT_CR=true.
	if os.Getenv("BASELINE_SECURITY_SKIP_DEFAULT_CR") != "true" {
		utilruntime.Must(mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
			if !mgr.GetCache().WaitForCacheSync(ctx) {
				return nil
			}
			list := &baselinev1alpha1.ClusterBaselineList{}
			if err := mgr.GetClient().List(ctx, list); err != nil || len(list.Items) > 0 {
				return nil
			}
			cb := &baselinev1alpha1.ClusterBaseline{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec:       baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
			}
			if err := mgr.GetClient().Create(ctx, cb); err != nil && !apierrors.IsAlreadyExists(err) {
				setupLog.Error(err, "creating default ClusterBaseline")
			}
			return nil
		})))
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
