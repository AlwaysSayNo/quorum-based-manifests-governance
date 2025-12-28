/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
)

// GovernanceQueueReconciler reconciles a GovernanceQueue object
type GovernanceQueueReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	logger logr.Logger
}

// SetupWithManager sets up the controller with the Manager.
func (r *GovernanceQueueReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&governancev1alpha1.GovernanceQueue{}).
		Watches(
			&governancev1alpha1.GovernanceEvent{},
			handler.EnqueueRequestsFromMapFunc(r.findQueueForEvent),
		).
		Named("governancequeue").
		Complete(r)
}

// findQueueForEvent is a mapping function to assign an Event to a Queue.
func (r *GovernanceQueueReconciler) findQueueForEvent(ctx context.Context, obj client.Object) []reconcile.Request {
	event, ok := obj.(*governancev1alpha1.GovernanceEvent)
	if !ok {
		// Not an event.
		return nil
	}

	// Get EventQueue from Client.
	eventKey := types.NamespacedName{Name: event.Name, Namespace: event.Namespace}
	r.logger.WithValues("event", eventKey, "type", event.Spec.Type, "eventMRT", event.Spec.MRT)

	_, queue, err := r.getMRTAndQueue(ctx, event)
	if err != nil {
		r.logger.Error(err, "Failed to get MRT/GovernanceQueue for GovernanceEvent in Watch function.")
		return nil
	}

	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name:      queue.Name,
				Namespace: queue.Namespace,
			},
		},
	}
}

// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=governanceevents,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=governanceevents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=governanceevents/finalizers,verbs=update
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=governancequeues,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=governancequeues/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=governancequeues/finalizers,verbs=update

func (r *GovernanceQueueReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = logf.FromContext(ctx).WithValues("controller", "GovernanceQueue", "name", req.Name, "namespace", req.Namespace)
	r.logger.Info("Reconciling GovernanceQueue")

	// Fetch the GovernanceQueue.
	queue := &governancev1alpha1.GovernanceQueue{}
	if err := r.Get(ctx, req.NamespacedName, queue); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Find all GovernanceEvent objects that belong to this queue.
	eventList, err := r.getAllEventsForQueue(ctx, queue)
	if err != nil {
		r.logger.Error(err, "Failed to list GovernanceEvents for queue")
		return ctrl.Result{}, fmt.Errorf("list GovernanceEvents for queue: %w", err)
	}

	// Sort the events by creation time to ensure the queue is ordered.
	sort.Slice(eventList, func(i, j int) bool {
		return eventList[i].CreationTimestamp.Before(&eventList[j].CreationTimestamp)
	})

	// Create queue of EventReferences.
	var desiredQueue []governancev1alpha1.EventReference
	for _, event := range eventList {
		// Only add events that are not being deleted.
		if event.ObjectMeta.DeletionTimestamp.IsZero() {
			desiredQueue = append(desiredQueue, governancev1alpha1.EventReference{
				UID:  event.UID,
				Name: event.Name,
			})
		}
	}

	// Compare the "desired" queue with "actual" queue and patch if different.
	if !reflect.DeepEqual(queue.Status.Queue, desiredQueue) {
		patch := client.MergeFrom(queue.DeepCopy())
		queue.Status.Queue = desiredQueue

		r.logger.Info("Queue is out of sync. Reconciling.", "desiredCount", len(desiredQueue), "actualCount", len(queue.Status.Queue))
		if err := r.Status().Patch(ctx, queue, patch); err != nil {
			r.logger.Error(err, "Failed to patch queue status")
			return ctrl.Result{}, fmt.Errorf("patch queue: %w", err)
		}
		r.logger.Info("Queue state", "state", queue.Status.Queue)
	} else {
		r.logger.Info("Queue is already in sync.")
	}

	return ctrl.Result{}, nil
}

func (r *GovernanceQueueReconciler) getAllEventsForQueue(ctx context.Context, queue *governancev1alpha1.GovernanceQueue) ([]governancev1alpha1.GovernanceEvent, error) {
	eventList := &governancev1alpha1.GovernanceEventList{}
	if err := r.List(ctx, eventList); err != nil {
		r.logger.Error(err, "Failed to list GovernanceEvents for queue")
		return nil, fmt.Errorf("list GovernanceEvents for queue", err)
	}

	mrtRef := queue.Spec.MRT
	result := []governancev1alpha1.GovernanceEvent{}
	for _, e := range eventList.Items {

		if mrtRef == e.Spec.MRT {
			result = append(result, e)
		}
	}

	return result, nil
}

func (r *GovernanceQueueReconciler) getMRTAndQueue(ctx context.Context, event *governancev1alpha1.GovernanceEvent) (*governancev1alpha1.ManifestRequestTemplate, *governancev1alpha1.GovernanceQueue, error) {
	// Get MRT for event.
	mrtRef := event.Spec.MRT
	var mrt governancev1alpha1.ManifestRequestTemplate
	if err := r.Get(ctx, types.NamespacedName{Namespace: mrtRef.Namespace, Name: mrtRef.Name}, &mrt); err != nil {
		return nil, nil, fmt.Errorf("fetch ManifestRequestTemplate for GovernanceEvent", err)
	}

	// Get GovernanceQueue for MRT.
	queueRef := mrt.Status.RevisionQueueRef
	var queue governancev1alpha1.GovernanceQueue
	if err := r.Get(ctx, types.NamespacedName{Namespace: queueRef.Namespace, Name: queueRef.Name}, &queue); err != nil {
		r.logger.Error(err, "Failed to get GovernanceQueue for MRT.", "mrt", mrtRef, "queue", queueRef)
		return &mrt, nil, fmt.Errorf("fetch ManifestRequestTemplate for ManifestRequestTemplate", err)
	}

	return &mrt, &queue, nil
}
