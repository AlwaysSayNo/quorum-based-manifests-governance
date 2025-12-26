package controller

import (
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
)

// StatusUpdatePredicate filters out Update events that only change the object's status (except revision queue).
// This is to prevent infinite reconciliation loops caused by own status updates.
type StatusUpdatePredicate struct {
	predicate.Funcs
}

func (StatusUpdatePredicate) Update(e event.UpdateEvent) bool {
	// 1. Always reconcile if the .spec changes. The generation is the best way to check this.
	if e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() {
		return true
	}

	// 2. At this point, we know the .spec has not changed. We only care about status changes.
	// Cast the objects to our specific type to access the Status field.
	oldMRT, okOld := e.ObjectOld.(*governancev1alpha1.ManifestRequestTemplate)
	newMRT, okNew := e.ObjectNew.(*governancev1alpha1.ManifestRequestTemplate)

	// If the cast fails, it's safer to reconcile.
	if !okOld || !okNew {
		return true
	}

	// 3. Compare the RevisionsQueue slices. If they are different, we MUST reconcile.
	if !reflect.DeepEqual(oldMRT.Status.RevisionsQueue, newMRT.Status.RevisionsQueue) {
		return true
	}

	// 4. If we reach here, the .spec is the same and the RevisionsQueue is the same.
	// Any other change must be a status-only update from our own controller (like changing conditions),
	// which we want to ignore to prevent loops.
	return false
}