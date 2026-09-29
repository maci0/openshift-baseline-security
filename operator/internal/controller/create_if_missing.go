package controller

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// createIfMissing creates obj and tolerates AlreadyExists. It backs the
// steps whose object is declared by the step rather than reconciled against a
// desired state: the operator namespace, the plugin namespace, and the
// Subscription. A half deleted one on a resync is recreated instead of
// wedging the step that finds it missing. The remaining owned objects go
// through controllerutil.CreateOrUpdate or a plain Create, since they are
// updated in place.
func createIfMissing(ctx context.Context, c client.Client, obj client.Object) error {
	if err := c.Create(ctx, obj); err != nil && !apierrors.IsAlreadyExists(err) {
		// Identity in the message: callers wrap with step names, but on-call still
		// needs which object Create rejected (namespace vs Subscription vs OG).
		if ns := obj.GetNamespace(); ns != "" {
			return fmt.Errorf("creating %s/%s: %w", ns, obj.GetName(), err)
		}
		return fmt.Errorf("creating %s: %w", obj.GetName(), err)
	}
	return nil
}
