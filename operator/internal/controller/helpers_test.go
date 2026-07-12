package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

func TestRequeueAfter(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	steady := &baselinev1alpha1.ClusterBaseline{}
	setCond(steady, "Progressing", metav1.ConditionFalse, "AsExpected", "")
	if got := requeueAfterAt(steady, now); got != time.Minute {
		t.Fatalf("steady = %v, want 1m", got)
	}
	installing := &baselinev1alpha1.ClusterBaseline{}
	setCond(installing, "Progressing", metav1.ConditionTrue, "Reconciling", "installing")
	if got := requeueAfterAt(installing, now); got != 15*time.Second {
		t.Fatalf("Progressing = %v, want 15s", got)
	}
	// In-flight batch must poll faster so cancel/grace/Applied are not stuck
	// behind the 1m steady cadence when the informer is lagging.
	batching := &baselinev1alpha1.ClusterBaseline{}
	setCond(batching, "Progressing", metav1.ConditionFalse, "AsExpected", "")
	batching.Status.RemediationBatch = &baselinev1alpha1.RemediationBatchStatus{Phase: "Applying"}
	if got := requeueAfterAt(batching, now); got != 15*time.Second {
		t.Fatalf("batch Applying = %v, want 15s", got)
	}
	// Production entry point matches requeueAfterAt(now) for a steady CR.
	if got := requeueAfter(steady); got != time.Minute {
		t.Fatalf("requeueAfter(steady) = %v, want 1m", got)
	}
}

func TestNearestWaiverExpiry(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	past := metav1.NewTime(now.Add(-time.Hour))
	soon := metav1.NewTime(now.Add(20 * time.Second))
	later := metav1.NewTime(now.Add(2 * time.Minute))
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{
			Waivers: []baselinev1alpha1.WaiverEntry{
				{Name: "expired", ExpiresAt: &past},
				{Name: "open"}, // no expiry
				{Name: "later", ExpiresAt: &later},
				{Name: "soon", ExpiresAt: &soon},
			},
		},
	}
	if got := nearestWaiverExpiry(cb, now); got != 20*time.Second {
		t.Fatalf("nearest = %v, want 20s", got)
	}
	if got := nearestWaiverExpiry(&baselinev1alpha1.ClusterBaseline{}, now); got != 0 {
		t.Fatalf("empty waivers = %v, want 0", got)
	}
	// Steady requeue shortens to the nearest active expiry (pinned clock: no
	// wall-clock lag window that used to soft-pass under load).
	setCond(cb, "Progressing", metav1.ConditionFalse, "AsExpected", "")
	const horizon = 45 * time.Second
	far := metav1.NewTime(now.Add(horizon))
	cb.Spec.Waivers = []baselinev1alpha1.WaiverEntry{{Name: "w", ExpiresAt: &far}}
	if got := requeueAfterAt(cb, now); got != horizon {
		t.Fatalf("steady+waiver expiry requeue = %v, want %v", got, horizon)
	}
	// Progressing stays at fast (15s) even when a waiver expires later than fast.
	setCond(cb, "Progressing", metav1.ConditionTrue, "Reconciling", "installing")
	laterAt := metav1.NewTime(now.Add(45 * time.Second))
	cb.Spec.Waivers = []baselinev1alpha1.WaiverEntry{{Name: "w", ExpiresAt: &laterAt}}
	if got := requeueAfterAt(cb, now); got != 15*time.Second {
		t.Fatalf("Progressing+later waiver = %v, want 15s (fast wins over 45s expiry)", got)
	}
	// Near-zero active expiry floors at 1s so clock skew cannot hot-loop.
	near := metav1.NewTime(now.Add(50 * time.Millisecond))
	setCond(cb, "Progressing", metav1.ConditionFalse, "AsExpected", "")
	cb.Spec.Waivers = []baselinev1alpha1.WaiverEntry{{Name: "w", ExpiresAt: &near}}
	if got := requeueAfterAt(cb, now); got != time.Second {
		t.Fatalf("near-expiry requeue = %v, want 1s floor", got)
	}
	// Already-expired (or exactly-now) waiver does not shorten steady poll.
	atNow := metav1.NewTime(now)
	cb.Spec.Waivers = []baselinev1alpha1.WaiverEntry{{Name: "w", ExpiresAt: &atNow}}
	if got := requeueAfterAt(cb, now); got != time.Minute {
		t.Fatalf("expired-at-now requeue = %v, want 1m steady", got)
	}
}

// FuzzNearestWaiverExpiry: waiver ExpiresAt is CR-editable. Result is 0 or a
// positive duration to an After(now) entry; never panics; open/expired ignored.
func FuzzNearestWaiverExpiry(f *testing.F) {
	nowUnix := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC).Unix()
	f.Add(int64(0), true, int64(0), false)        // both open / zero
	f.Add(nowUnix+20, false, nowUnix+120, false)  // two future
	f.Add(nowUnix-3600, false, nowUnix+20, false) // past + future
	f.Add(nowUnix+1, false, nowUnix-1, true)      // future + open
	f.Fuzz(func(t *testing.T, aUnix int64, aOpen bool, bUnix int64, bOpen bool) {
		now := time.Unix(nowUnix, 0).UTC()
		// Bound so time math stays well-defined under the fuzzer.
		clamp := func(u int64) int64 {
			if u < 0 {
				u = -u
			}
			return u % (1 << 40)
		}
		var waivers []baselinev1alpha1.WaiverEntry
		if aOpen {
			waivers = append(waivers, baselinev1alpha1.WaiverEntry{Name: "a"})
		} else {
			exp := metav1.NewTime(time.Unix(clamp(aUnix), 0).UTC())
			waivers = append(waivers, baselinev1alpha1.WaiverEntry{Name: "a", ExpiresAt: &exp})
		}
		if bOpen {
			waivers = append(waivers, baselinev1alpha1.WaiverEntry{Name: "b"})
		} else {
			exp := metav1.NewTime(time.Unix(clamp(bUnix), 0).UTC())
			waivers = append(waivers, baselinev1alpha1.WaiverEntry{Name: "b", ExpiresAt: &exp})
		}
		cb := &baselinev1alpha1.ClusterBaseline{
			Spec: baselinev1alpha1.ClusterBaselineSpec{Waivers: waivers},
		}
		got := nearestWaiverExpiry(cb, now)
		if got < 0 {
			t.Fatalf("nearestWaiverExpiry negative: %v", got)
		}
		// Oracle: min positive duration among After(now) entries, else 0.
		var want time.Duration
		for i := range cb.Spec.Waivers {
			exp := cb.Spec.Waivers[i].ExpiresAt
			if exp == nil || !exp.After(now) {
				continue
			}
			d := exp.Sub(now)
			if want == 0 || d < want {
				want = d
			}
		}
		if got != want {
			t.Fatalf("got %v want %v (aOpen=%v bOpen=%v a=%d b=%d)", got, want, aOpen, bOpen, aUnix, bUnix)
		}
	})
}

func TestCreateIfMissing(t *testing.T) {
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "create-if-missing-test"}}
	if err := createIfMissing(context.Background(), c, ns); err != nil {
		t.Fatal(err)
	}
	got := &corev1.Namespace{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: ns.Name}, got); err != nil {
		t.Fatal(err)
	}
	again := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns.Name}}
	if err := createIfMissing(context.Background(), c, again); err != nil {
		t.Fatal("AlreadyExists should be ignored:", err)
	}
}

// TestCreateIfMissingErrorIdentity: non-AlreadyExists Create failures must name
// the object so on-call can tell namespace vs Subscription vs OperatorGroup.
func TestCreateIfMissingErrorIdentity(t *testing.T) {
	scheme := testScheme(t)
	deny := errors.New("injected create denial")
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				return deny
			},
		}).Build()

	// Cluster-scoped: "creating <name>: …"
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "target-ns"}}
	err := createIfMissing(context.Background(), c, ns)
	if err == nil {
		t.Fatal("expected create error")
	}
	if got := err.Error(); !strings.Contains(got, "creating target-ns:") || !strings.Contains(got, deny.Error()) {
		t.Fatalf("cluster-scoped wrap = %q, want creating target-ns: …", got)
	}

	// Namespaced: "creating <ns>/<name>: …"
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "openshift-compliance"}}
	err = createIfMissing(context.Background(), c, cm)
	if err == nil {
		t.Fatal("expected create error")
	}
	if got := err.Error(); !strings.Contains(got, "creating openshift-compliance/cfg:") {
		t.Fatalf("namespaced wrap = %q, want creating openshift-compliance/cfg: …", got)
	}

	// AlreadyExists is still swallowed (identity wrap must not reclassify it).
	exists := fake.NewClientBuilder().WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				return apierrors.NewAlreadyExists(schema.GroupResource{Resource: "namespaces"}, "x")
			},
		}).Build()
	if err := createIfMissing(context.Background(), exists, ns); err != nil {
		t.Fatal("AlreadyExists must be ignored:", err)
	}
}
