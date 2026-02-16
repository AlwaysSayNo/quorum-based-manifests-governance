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
	"slices"
	"sort"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
)

const (
	QubmangoMRTUIDAnnotation = "governance.nazar.grynko.com/mrt-uid"
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
	r.logger.V(2).Info("Reconciling GovernanceQueue")

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
				UID:       event.UID,
				Name:      event.Name,
				Namespace: event.Namespace,
			})
		}
	}

	// Compare the "desired" queue with "actual" queue and patch if different.
	if !reflect.DeepEqual(queue.Status.Queue, desiredQueue) {
		patch := client.MergeFrom(queue.DeepCopy())
		queue.Status.Queue = desiredQueue

		r.logger.V(2).Info("Queue is out of sync. Reconciling.", "desiredCount", len(desiredQueue), "actualCount", len(queue.Status.Queue))
		if err := r.Status().Patch(ctx, queue, patch); err != nil {
			r.logger.Error(err, "Failed to patch queue status")
			return ctrl.Result{}, fmt.Errorf("patch queue: %w", err)
		}
		r.logger.V(2).Info("Queue state", "state", queue.Status.Queue)
	} else {
		r.logger.V(2).Info("Queue is already in sync.")
	}

	return ctrl.Result{}, nil
}

func (r *GovernanceQueueReconciler) getAllEventsForQueue(ctx context.Context, queue *governancev1alpha1.GovernanceQueue) ([]governancev1alpha1.GovernanceEvent, error) {
	eventList := &governancev1alpha1.GovernanceEventList{}
	mrtRef := queue.Spec.MRT
	var mrt governancev1alpha1.ManifestRequestTemplate
	if err := r.Get(ctx, types.NamespacedName{Name: mrtRef.Name, Namespace: mrtRef.Namespace}, &mrt); err != nil {
		r.logger.Error(err, "Failed to fetch MRT for GovernanceQueue", "mrtRef", mrtRef)
		return nil, fmt.Errorf("fetch ManifestRequestTemplate for GovernanceQueue")
	}

	// Use label for quick search
	matchingLabels := client.MatchingLabels{
		QubmangoMRTUIDAnnotation: string(mrt.UID),
	}

	if err := r.List(ctx, eventList, matchingLabels); err != nil {
		r.logger.Error(err, "Failed to list GovernanceEvents for queue")
		return nil, fmt.Errorf("list GovernanceEvents for queue: %w", err)
	}
	return eventList.Items, nil
}

func (r *GovernanceQueueReconciler) getMRTAndQueue(ctx context.Context, event *governancev1alpha1.GovernanceEvent) (*governancev1alpha1.ManifestRequestTemplate, *governancev1alpha1.GovernanceQueue, error) {
	// Get MRT for event.
	mrtRef := event.Spec.MRT
	var mrt governancev1alpha1.ManifestRequestTemplate
	if err := r.Get(ctx, types.NamespacedName{Namespace: mrtRef.Namespace, Name: mrtRef.Name}, &mrt); err != nil {
		return nil, nil, fmt.Errorf("fetch ManifestRequestTemplate for GovernanceEvent: %w", err)
	}

	// Get GovernanceQueue for MRT.
	queueRef := mrt.Status.RevisionQueueRef
	var queue governancev1alpha1.GovernanceQueue
	if err := r.Get(ctx, types.NamespacedName{Namespace: queueRef.Namespace, Name: queueRef.Name}, &queue); err != nil {
		r.logger.Error(err, "Failed to get GovernanceQueue for MRT.", "mrt", mrtRef, "queue", queueRef)
		return &mrt, nil, fmt.Errorf("fetch GovernanceQueue for ManifestRequestTemplate: %w", err)
	}

	return &mrt, &queue, nil
}

func QueueContainsRevision(
	ctx context.Context,
	k8sClient client.Client,
	revision *string,
	mrtUID string,
) (bool, error) {
	events, err := getAllEventsForQueue(ctx, k8sClient, mrtUID)
	if err != nil {
		return false, fmt.Errorf("get all events for queue: %w", err)
	}

	eventIdx := slices.IndexFunc(events, func(e governancev1alpha1.GovernanceEvent) bool {
		return e.Spec.NewRevision != nil && e.Spec.NewRevision.CommitSHA == *revision
	})
	return eventIdx != -1, nil
}

func getAllEventsForQueue(
	ctx context.Context,
	k8sClient client.Client,
	mrtUID string,
) ([]governancev1alpha1.GovernanceEvent, error) {
	eventList := &governancev1alpha1.GovernanceEventList{}

	// Use label for quick search
	matchingLabels := client.MatchingLabels{
		QubmangoMRTUIDAnnotation: string(mrtUID),
	}

	if err := k8sClient.List(ctx, eventList, matchingLabels); err != nil {
		return nil, fmt.Errorf("list GovernanceEvents for queue: %w", err)
	}
	return eventList.Items, nil
}

func RevisionsQueueHead(
	ctx context.Context,
	k8sClient client.Client,
	queueRef governancev1alpha1.ManifestRefOptional,
) (*governancev1alpha1.GovernanceEvent, error) {
	// Get the queue.
	var queue governancev1alpha1.GovernanceQueue
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: queueRef.Namespace, Name: queueRef.Name}, &queue); err != nil {
		return nil, fmt.Errorf("get GovernanceQueue for ManifestRequestTemplate: %w", err)
	}

	// Check if the queue is empty.
	if len(queue.Status.Queue) < 1 {
		return nil, fmt.Errorf("queue is empty: %v", queueRef)
	}

	// Fetch the head GovernanceEvent object.
	eventRef := queue.Status.Queue[0]
	var event governancev1alpha1.GovernanceEvent
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: eventRef.Name, Namespace: eventRef.Namespace}, &event); err != nil {
		return nil, fmt.Errorf("get head GovernanceEvent for GovernanceQueue: %w", err)
	}

	if event.Spec.Type != governancev1alpha1.EventTypeNewRevision {
		return nil, fmt.Errorf("unsupported event type: %s", event.Spec.Type)
	}

	return &event, nil
}

func RemoveRevisionsQueueHead(
	ctx context.Context,
	k8sClient client.Client,
	queueRef governancev1alpha1.ManifestRefOptional,
) error {
	// Get the revision we are working on.
	event, err := RevisionsQueueHead(ctx, k8sClient, queueRef)
	if err != nil {
		return fmt.Errorf("get head of the revision queue: %w", err)
	}

	// Delete the resource and trigger reconciliation for GovernanceQueue.
	if err = k8sClient.Delete(ctx, event); err != nil {
		return fmt.Errorf("delete GovernanceEvent: %w", err)
	}

	return nil
}

func CreateNewRevisionEvent(
	ctx context.Context,
	k8sClient client.Client,
	revision *string,
	mrtRef *governancev1alpha1.ManifestRef,
	mrtUID string,
) error {
	// Create a stable, unique name.
	eventName := fmt.Sprintf("event-%s-%s-%s", mrtRef.Name, mrtRef.Namespace, *revision)

	revisionEvent := governancev1alpha1.GovernanceEvent{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: mrtRef.Namespace,
			Labels: map[string]string{
				QubmangoMRTUIDAnnotation: string(mrtUID), // Use UID for a unique, immutable link
			},
		},
		Spec: governancev1alpha1.GovernanceEventSpec{
			Type: governancev1alpha1.EventTypeNewRevision,
			MRT:  *mrtRef,
			NewRevision: &governancev1alpha1.NewRevisionPayload{
				CommitSHA: *revision,
			},
		},
	}

	if err := k8sClient.Create(ctx, &revisionEvent); err != nil {
		return fmt.Errorf("create new revision %s event: %w", *revision, err)
	}

	return nil
}
