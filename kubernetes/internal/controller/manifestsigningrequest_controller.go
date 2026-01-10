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
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/validation"
	commonvalidation "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/validation"
	validationmsr "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/validation/msr"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	"github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/notifier"
	repomanager "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository"
)

const ScheduledInterval = 1 * time.Minute

// MSRStateHandler defines a function that performs work within a state and returns the next state
type MSRStateHandler func(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (governancev1alpha1.MSRActionState, error)

// MSRReconcileStateHandler defines a function that performs work within a reconcile state
// and returns the next reconcile state
type MSRReconcileStateHandler func(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (governancev1alpha1.MSRReconcileNewMSRSpecState, error)

// MSRRulesFulfillmentStateHandler defines a function that performs work within a rules fulfillment state
// and returns the next rules fulfillment state
type MSRRulesFulfillmentStateHandler func(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (governancev1alpha1.MSRRulesFulfillmentState, error)

// ManifestSigningRequestReconciler reconciles a ManifestSigningRequest object
type ManifestSigningRequestReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	RepoManager RepositoryManager
	Notifier    notifier.Notifier
	logger      logr.Logger
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManifestSigningRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&governancev1alpha1.ManifestSigningRequest{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Named("manifestsigningrequest").
		Complete(r)
}

// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestsigningrequests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestsigningrequests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestsigningrequests/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile orchestrates the reconciliation flow for ManifestSigningRequest.
// It handles three main paths:
// 1. Deletion: Remove finalizer and cleanup
// 2. Initialization: Create, push to Git, update history, set finalizer
// 3. Normal: Process new versions and respond to signature events
func (r *ManifestSigningRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = logf.FromContext(ctx).WithValues("controller", "ManifestSigningRequest", "name", req.Name, "namespace", req.Namespace)
	r.logger.Info("Starting reconciliation", "msr", req.NamespacedName)

	// Fetch the MSR instance
	msr := &governancev1alpha1.ManifestSigningRequest{}
	if err := r.Get(ctx, req.NamespacedName, msr); err != nil {
		r.logger.V(2).Info("MSR not found, may have been deleted", "err", err)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if !msr.ObjectMeta.DeletionTimestamp.IsZero() {
		r.logger.Info("MSR is marked for deletion", "actionState", msr.Status.ActionState)
		return r.reconcileDelete(ctx, msr)
	}

	// Release lock if hold too long (deadlock prevention)
	if r.isLockForMoreThan(msr, 30*time.Second) {
		r.logger.Info("Lock held too long, releasing to prevent deadlock", "actionState", msr.Status.ActionState, "lockDuration", "30s")
		return ctrl.Result{Requeue: true}, r.releaseLockWithFailure(ctx, msr, msr.Status.ActionState, fmt.Errorf("lock acquired for too long"))
	}

	// Handle initialization (before finalizer is set)
	if !controllerutil.ContainsFinalizer(msr, GovernanceFinalizer) {
		r.logger.Info("MSR not initialized, starting initialization flow", "actionState", msr.Status.ActionState)
		return r.reconcileCreate(ctx, msr, req)
	}

	// Handle normal reconciliation (after initialization)
	r.logger.Info("MSR initialized, processing normal reconciliation", "actionState", msr.Status.ActionState)
	result, err := r.reconcileNormal(ctx, msr, req)

	return r.handleResult(result, err)
}

// reconcileDelete handles cleanup when MSR is marked for deletion.
// It removes the finalizer, allowing Kubernetes to delete the resource.
func (r *ManifestSigningRequestReconciler) reconcileDelete(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(msr, GovernanceFinalizer) {
		// No custom finalizer found, nothing to clean up
		r.logger.V(2).Info("No finalizer found, deletion already in progress")
		return ctrl.Result{}, nil
	}

	r.logger.Info("Processing MSR deletion", "actionState", msr.Status.ActionState)

	switch msr.Status.ActionState {
	default:
		// Any state means we start the deletion process
		return r.handleStateDeletion(ctx, msr)
	}
}

// handleStateDeletion removes the finalizer from MSR to complete deletion.
// State: any → MSREmptyActionState
func (r *ManifestSigningRequestReconciler) handleStateDeletion(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	r.logger.Info("Starting finalizer removal", "currentState", msr.Status.ActionState)

	// Remove the finalizer from MSR
	controllerutil.RemoveFinalizer(msr, GovernanceFinalizer)
	if err := r.Update(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to remove finalizer")
		_ = r.releaseLockWithFailure(ctx, msr, governancev1alpha1.MSRActionStateDeletion, err)
		return ctrl.Result{}, fmt.Errorf("remove finalizer from MSRStateDeletionInProgress: %w", err)
	}

	r.logger.Info("Finalizer removed successfully")
	err := r.releaseLockAndSetNextState(ctx, msr, governancev1alpha1.MSRActionStateEmpty)
	if err != nil {
		r.logger.Error(err, "Failed to release lock after deletion")
	}
	return ctrl.Result{}, err
}

// isLockForMoreThan checks if the Progressing lock has been held for longer than the specified duration.
// Used for deadlock detection and prevention.
func (r *ManifestSigningRequestReconciler) isLockForMoreThan(
	msr *governancev1alpha1.ManifestSigningRequest,
	duration time.Duration,
) bool {
	condition := meta.FindStatusCondition(msr.Status.Conditions, governancev1alpha1.Progressing)
	return condition != nil && condition.Status == metav1.ConditionTrue && time.Now().Sub(condition.LastTransitionTime.Time) >= duration
}

// reconcileCreate handles initialization of a new MSR.
// It progresses through these states:
// 1. MSRStateGitPushMSR: Push initial MSR manifest to Git
// 2. MSRStateUpdateAfterGitPush: Update in-cluster history after push
// 3. MSRStateInitSetFinalizer: Set finalizer to complete initialization
func (r *ManifestSigningRequestReconciler) reconcileCreate(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
	req ctrl.Request,
) (ctrl.Result, error) {
	r.logger.Info("Initializing new MSR", "actionState", msr.Status.ActionState)

	// Check if there is any available git repository provider
	if _, err := r.repositoryWithError(ctx, msr); err != nil {
		r.logger.Error(err, "Repository provider unavailable, cannot proceed with initialization")
		return ctrl.Result{}, fmt.Errorf("init repo for ManifestSigningRequest: %w", err)
	}

	switch msr.Status.ActionState {
	case governancev1alpha1.MSRActionStateEmpty, governancev1alpha1.MSRActionStateGitPushMSR:
		// 1. Create an initial MSR file in governance folder in Git repository.
		r.logger.Info("Pushing initial MSR to Git repository")
		return r.handleStateGitCommit(ctx, msr)
	case governancev1alpha1.MSRActionStateGovernorQubmangoSignature:
		// 2. Create and push qubmango MSR signature as an initial governor.
		r.logger.Info("Pushing qubmango governor MSR signature to Git repository")
		return r.handleStateGovernorQubmangoSignature(ctx, msr)
	case governancev1alpha1.MSRActionStateUpdateAfterGitPush:
		// 3. Update information in-cluster MSR (add history record) after Git repository push.
		r.logger.Info("Updating MSR after initial Git push")
		return r.handleUpdateAfterGitPush(ctx, msr)
	case governancev1alpha1.MSRActionStateInitSetFinalizer:
		// 4. Confirm MSR initialization by setting the GovernanceFinalizer.
		r.logger.Info("Setting finalizer to complete initialization")
		return r.handleStateFinalizing(ctx, msr)
	default:
		err := fmt.Errorf("unknown initialization state: %s", string(msr.Status.ActionState))
		r.logger.Error(err, "Invalid state for initialization")
		return ctrl.Result{}, err
	}
}

// handleStateGitCommit pushes the initial MSR manifest to the Git repository.
// State: MSRStateGitPushMSR → MSRActionStateGovernorQubmangoSignature
func (r *ManifestSigningRequestReconciler) handleStateGitCommit(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	return r.withLock(ctx, msr, governancev1alpha1.MSRActionStateGitPushMSR, "Pushing MSR manifest to Git repository",
		func(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (governancev1alpha1.MSRActionState, error) {
			r.logger.Info("Saving initial MSR to Git repository", "msr", msr.Name, "namespace", msr.Namespace)
			if err := r.saveInRepository(ctx, msr); err != nil {
				r.logger.Error(err, "Failed to save MSR to repository")
				return "", fmt.Errorf("save MSR to Git repository: %w", err)
			}
			r.logger.Info("MSR saved to repository successfully")
			return governancev1alpha1.MSRActionStateGovernorQubmangoSignature, nil
		},
	)
}

// handleStateQubmangoSignature pushes its own signature, as a governor of MSR to the Git repository.
// State: MSRActionStateGovernorQubmangoSignature → MSRStateUpdateAfterGitPush
func (r *ManifestSigningRequestReconciler) handleStateGovernorQubmangoSignature(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	return r.withLock(ctx, msr, governancev1alpha1.MSRActionStateGovernorQubmangoSignature, "Pushing qubmango governor MSR signature to Git repository",
		func(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (governancev1alpha1.MSRActionState, error) {
			r.logger.Info("Saving qubmango governor MSR signature to Git repository", "msr", msr.Name, "namespace", msr.Namespace)
			repositoryMSR := r.createRepositoryMSR(msr)

			if _, err := r.repository(ctx, msr).PushGovernorSignature(ctx, &repositoryMSR); err != nil {
				r.logger.Error(err, "Failed to save qubmango governor MSR signature to repository")
				return "", fmt.Errorf("save qubmango governor MSR signature to Git repository: %w", err)
			}
			r.logger.Info("Qubmango governor MSR signature saved to repository successfully")
			return governancev1alpha1.MSRActionStateUpdateAfterGitPush, nil
		},
	)
}

// handleUpdateAfterGitPush updates the in-cluster MSR status with history after Git push.
// State: MSRStateUpdateAfterGitPush → MSRStateInitSetFinalizer
func (r *ManifestSigningRequestReconciler) handleUpdateAfterGitPush(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	return r.withLock(ctx, msr, governancev1alpha1.MSRActionStateUpdateAfterGitPush, "Updating in-cluster MSR information after Git push",
		func(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (governancev1alpha1.MSRActionState, error) {
			r.logger.Info("Adding history record after Git push", "version", msr.Spec.Version, "newHistoryCount", len(msr.Status.RequestHistory)+1)
			newRecord := r.createNewMSRHistoryRecordFromSpec(msr)
			msr.Status.RequestHistory = append(msr.Status.RequestHistory, newRecord)
			if err := r.Status().Update(ctx, msr); err != nil {
				r.logger.Error(err, "Failed to update MSR with history record", "version", newRecord.Version)
				return "", fmt.Errorf("update MSR information after Git push: %w", err)
			}
			r.logger.Info("MSR history updated successfully")
			return governancev1alpha1.MSRActionStateInitSetFinalizer, nil
		},
	)
}

// handleStateFinalizing sets the GovernanceFinalizer on the MSR to complete initialization.
// State: MSRStateInitSetFinalizer → MSREmptyActionState
func (r *ManifestSigningRequestReconciler) handleStateFinalizing(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	return r.withLock(ctx, msr, governancev1alpha1.MSRActionStateInitSetFinalizer, "Setting the GovernanceFinalizer on MSR",
		func(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (governancev1alpha1.MSRActionState, error) {
			r.logger.Info("Adding GovernanceFinalizer to complete initialization")
			controllerutil.AddFinalizer(msr, GovernanceFinalizer)
			if err := r.Update(ctx, msr); err != nil {
				r.logger.Error(err, "Failed to add finalizer")
				return "", fmt.Errorf("add finalizer in initialization: %w", err)
			}
			r.logger.Info("Initialization complete, finalizer added")
			return governancev1alpha1.MSRActionStateEmpty, nil
		},
	)
}

// handleResult acts as a middleware to ensure polling is applied on success
func (r *ManifestSigningRequestReconciler) handleResult(
	result ctrl.Result,
	err error,
) (ctrl.Result, error) {
	// If there is an error, let the controller-runtime handle exponential backoff.
	if err != nil {
		return result, err
	}

	// Skip explicitly requested Requeue.
	if result.Requeue || result.RequeueAfter > 0 {
		return result, nil
	}

	// If the logic returned empty result - use Scheduled interval to requeue later.
	return ctrl.Result{RequeueAfter: ScheduledInterval}, nil
}

// reconcileNormal handles reconciliation for initialized MSR resources.
// It processes new versions detected in the Spec and orchestrates the reconciliation state machine.
// If Spec.Version > latest history version, transitions to MSRReconcileNewMSRSpec state.
func (r *ManifestSigningRequestReconciler) reconcileNormal(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
	req ctrl.Request,
) (ctrl.Result, error) {
	// Check for active lock
	if meta.IsStatusConditionTrue(msr.Status.Conditions, governancev1alpha1.Progressing) {
		r.logger.V(2).Info("Waiting for ongoing operation to complete", "actionState", msr.Status.ActionState)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// If ActionState is empty, we need to decide what action to do
	if msr.Status.ActionState == governancev1alpha1.MSRActionStateEmpty {
		// Get the latest history record version
		latestHistoryVersion := -1
		history := msr.Status.RequestHistory
		if len(history) > 0 {
			latestHistoryVersion = history[len(history)-1].Version
		}

		if msr.Spec.Version > latestHistoryVersion {
			// If the spec's version is newer - reconcile status
			r.logger.Info("New version detected in spec, starting reconciliation", "specVersion", msr.Spec.Version, "latestVersion", latestHistoryVersion)
			return ctrl.Result{Requeue: true}, r.releaseLockAndSetNextState(ctx, msr, governancev1alpha1.MSRActionStateNewMSRSpec)
		} else if msr.Status.Status == governancev1alpha1.InProgress {
			// Otherwise check the completeness of MSR rules
			r.logger.Info("Check the completeness of MSR rules", "version", msr.Spec.Version)
			return ctrl.Result{Requeue: true}, r.releaseLockAndSetNextState(ctx, msr, governancev1alpha1.MSRActionStateMSRRulesFulfillment)
		}

		// No new version detected
		r.logger.V(3).Info("No new version to process", "specVersion", msr.Spec.Version, "latestVersion", latestHistoryVersion)
	}

	// Execute action based on current ActionState
	switch msr.Status.ActionState {
	case governancev1alpha1.MSRActionStateNewMSRSpec:
		r.logger.V(2).Info("Processing new MSR spec")
		return r.handleReconcileNewMSRSpec(ctx, msr)
	case governancev1alpha1.MSRActionStateMSRRulesFulfillment:
		r.logger.V(2).Info("Checking MSR rules fulfillment")
		return r.handleReconcileMSRRulesFulfillment(ctx, msr)
	default:
		// If the state is unknown - reset to EmptyState
		r.logger.V(2).Info("Unknown action state, resetting", "actionState", msr.Status.ActionState)
		return ctrl.Result{}, r.releaseLockAndSetNextState(ctx, msr, governancev1alpha1.MSRActionStateEmpty)
	}
}

// handleReconcileNewMSRSpec orchestrates processing of a new MSR Spec version.
// State: MSRReconcileNewMSRSpec with sub-states:
// 1. MSRReconcileStateGitPushMSR: Push updated MSR to Git
// 2. MSRReconcileRStateUpdateAfterGitPush: Add history record
// 3. MSRReconcileRStateNotifyGovernors: Notify governors
// Final state: MSREmptyActionState
func (r *ManifestSigningRequestReconciler) handleReconcileNewMSRSpec(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	r.logger.Info("Starting reconciliation of new MSR spec", "version", msr.Spec.Version, "reconcileState", msr.Status.ReconcileState)

	// Acquire Lock
	lockAcquired, err := r.acquireLock(ctx, msr, governancev1alpha1.MSRActionStateNewMSRSpec, "Processing new MSR Spec")
	if !lockAcquired || err != nil {
		r.logger.V(2).Info("Lock already held, requeuing", "reconcileState", msr.Status.ReconcileState)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	// Check repository connection exists
	if _, err := r.repositoryWithError(ctx, msr); err != nil {
		r.logger.Error(err, "Repository unavailable for spec reconciliation")
		_ = r.releaseLockWithFailure(ctx, msr, governancev1alpha1.MSRActionStateNewMSRSpec, fmt.Errorf("check repository: %w", err))
		return ctrl.Result{}, err
	}

	// Dispatch to the correct reconcile sub-state handler
	// Each handler manages its own ReconcileState transition and lock release
	r.logger.V(2).Info("Dispatching to reconcile sub-state handler", "reconcileState", msr.Status.ReconcileState)
	switch msr.Status.ReconcileState {
	case governancev1alpha1.MSRReconcileNewMSRSpecStateEmpty, governancev1alpha1.MSRReconcileNewMSRSpecStateGitPushMSR:
		return r.handleMSRReconcileStateGitCommit(ctx, msr)
	case governancev1alpha1.MSRReconcileNewMSRSpecStateUpdateAfterGitPush:
		return r.handleMSRReconcileStateUpdateAfterGitPush(ctx, msr)
	case governancev1alpha1.MSRReconcileNewMSRSpecStateNotifyGovernors:
		return r.handleMSRReconcileStateNotifyGovernors(ctx, msr)
	default:
		err := fmt.Errorf("unknown ReconcileState: %s", string(msr.Status.ReconcileState))
		r.logger.Error(err, "Invalid reconcile state")
		_ = r.releaseLockWithFailure(ctx, msr, governancev1alpha1.MSRActionStateNewMSRSpec, err)
		return ctrl.Result{}, err
	}
}

// handleMSRReconcileStateGitCommit pushes the updated MSR manifest to Git repository.
// Sub-state: MSRReconcileStateGitPushMSR → MSRReconcileRStateUpdateAfterGitPush
func (r *ManifestSigningRequestReconciler) handleMSRReconcileStateGitCommit(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	return r.withReconcileLock(ctx, msr, governancev1alpha1.MSRActionStateNewMSRSpec, governancev1alpha1.MSRActionStateNewMSRSpec,
		func(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (governancev1alpha1.MSRReconcileNewMSRSpecState, error) {
			r.logger.Info("Pushing updated MSR manifest to Git repository", "version", msr.Spec.Version)
			if err := r.saveInRepository(ctx, msr); err != nil {
				r.logger.Error(err, "Failed to push MSR to repository", "version", msr.Spec.Version)
				return "", fmt.Errorf("save MSR in Git repository: %w", err)
			}
			r.logger.Info("MSR pushed to repository, transitioning to update state", "version", msr.Spec.Version)
			return governancev1alpha1.MSRReconcileNewMSRSpecStateUpdateAfterGitPush, nil
		},
	)
}

// handleMSRReconcileStateUpdateAfterGitPush adds a history record after Git push and sets status to InProgress.
// Sub-state: MSRReconcileRStateUpdateAfterGitPush → MSRReconcileRStateNotifyGovernors
func (r *ManifestSigningRequestReconciler) handleMSRReconcileStateUpdateAfterGitPush(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	return r.withReconcileLock(ctx, msr, governancev1alpha1.MSRActionStateNewMSRSpec, governancev1alpha1.MSRActionStateNewMSRSpec,
		func(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (governancev1alpha1.MSRReconcileNewMSRSpecState, error) {
			r.logger.Info("Adding reconciliation history record", "version", msr.Spec.Version)
			newRecord := r.createNewMSRHistoryRecordFromSpec(msr)
			msr.Status.RequestHistory = append(msr.Status.RequestHistory, newRecord)
			msr.Status.Status = governancev1alpha1.InProgress
			if err := r.Status().Update(ctx, msr); err != nil {
				r.logger.Error(err, "Failed to add history record", "version", newRecord.Version)
				return "", fmt.Errorf("update MSR information after Git push: %w", err)
			}
			r.logger.Info("History record added, ready to notify governors", "version", newRecord.Version, "newHistoryCount", len(msr.Status.RequestHistory))
			return governancev1alpha1.MSRReconcileNewMSRSpecStateNotifyGovernors, nil
		},
	)
}

// handleMSRReconcileStateNotifyGovernors sends notifications to governors about the new MSR version.
// Sub-state: MSRReconcileRStateNotifyGovernors → MSRReconcileStateRevisionEmpty (final)
// Transitions ActionState back to MSREmptyActionState, completing the reconciliation cycle.
func (r *ManifestSigningRequestReconciler) handleMSRReconcileStateNotifyGovernors(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	return r.withReconcileLock(ctx, msr, governancev1alpha1.MSRActionStateNewMSRSpec, governancev1alpha1.MSRActionStateEmpty,
		func(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (governancev1alpha1.MSRReconcileNewMSRSpecState, error) {
			r.logger.Info("Sending notifications to governors", "version", msr.Spec.Version, "requireSignatures", msr.Spec.Require)
			if err := r.Notifier.NotifyGovernorsMSR(ctx, msr); err != nil {
				r.logger.Error(err, "Failed to send notifications to governors", "version", msr.Spec.Version)
				return "", fmt.Errorf("send notification to governors: %w", err)
			}
			r.logger.Info("Notifications sent successfully, spec reconciliation complete", "version", msr.Spec.Version)
			return governancev1alpha1.MSRReconcileNewMSRSpecStateEmpty, nil
		},
	)
}

// saveInRepository pushes the MSR manifest to the configured Git repository.
// It creates a repository-friendly representation and returns the commit reference.
func (r *ManifestSigningRequestReconciler) saveInRepository(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) error {
	repositoryMSR := r.createRepositoryMSR(msr)
	if _, err := r.repository(ctx, msr).PushMSR(ctx, &repositoryMSR); err != nil {
		r.logger.Error(err, "Failed to push MSR to repository", "msr", msr.Name, "namespace", msr.Namespace)
		return fmt.Errorf("push initial ManifestSigningRequest to repository: %w", err)
	}

	return nil
}

// createRepositoryMSR transforms the MSR into a repository-compatible manifest object.
// It preserves the metadata and spec for storage in Git.
func (r *ManifestSigningRequestReconciler) createRepositoryMSR(
	msr *governancev1alpha1.ManifestSigningRequest,
) governancev1alpha1.ManifestSigningRequestManifestObject {
	msrCopy := msr.DeepCopy()
	return governancev1alpha1.ManifestSigningRequestManifestObject{
		TypeMeta: metav1.TypeMeta{
			Kind:       msrCopy.Kind,
			APIVersion: msrCopy.APIVersion,
		},
		ObjectMeta: governancev1alpha1.ManifestRef{
			Name:      msrCopy.Name,
			Namespace: msrCopy.Namespace,
		},
		Spec: msrCopy.Spec,
	}
}

// saveNewHistoryRecord creates and appends a new history record from the current spec.
// It updates the status with the new record, preserving the entire history.
func (r *ManifestSigningRequestReconciler) saveNewHistoryRecord(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) error {
	// Append a new history record
	newRecord := r.createNewMSRHistoryRecordFromSpec(msr)
	msr.Status.RequestHistory = append(msr.Status.RequestHistory, newRecord)

	// Save the change
	if err := r.Status().Update(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to update MSR status with new history record", "version", newRecord.Version)
		return fmt.Errorf("update ManifestSigningRequest status with new history record: %w", err)
	}

	r.logger.V(2).Info("New history record added", "version", newRecord.Version, "newHistoryCount", len(msr.Status.RequestHistory))
	return nil
}

// createNewMSRHistoryRecordFromSpec creates a history record from the current MSR spec.
// It captures the version, changes, governors, signature requirements, and current approval status.
func (r *ManifestSigningRequestReconciler) createNewMSRHistoryRecordFromSpec(
	msr *governancev1alpha1.ManifestSigningRequest,
) governancev1alpha1.ManifestSigningRequestHistoryRecord {
	msrCopy := msr.DeepCopy()

	return governancev1alpha1.ManifestSigningRequestHistoryRecord{
		CommitSHA:         msrCopy.Spec.CommitSHA,
		PreviousCommitSHA: msrCopy.Spec.PreviousCommitSHA,
		Version:           msrCopy.Spec.Version,
		Changes:           msrCopy.Spec.Changes,
		Governors:         msrCopy.Spec.Governors,
		Require:           msrCopy.Spec.Require,
	}
}

// handleReconcileMSRRulesFulfillment orchestrates processing of MSR rules fulfillment.
// State: MSRActionStateMSRRulesFulfillment with sub-states:
// 1. MSRRulesFulfillmentStateCheckSignatures: Verifies the fulfillment of signature rules and, if successful, saves fulfillment signatures and transfers to Approved status
// 2. MSRRulesFulfillmentStateUpdateMCASpec: Updates MCA spec with newest Approved data
// 3. MSRRulesFulfillmentStateNotifyGovernors: Notify governors
// Final state: MSREmptyActionState
func (r *ManifestSigningRequestReconciler) handleReconcileMSRRulesFulfillment(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	r.logger.Info("Starting reconciliation of MSR rules fulfillment", "version", msr.Spec.Version, "rulesFulfillmentState", msr.Status.RulesFulfillmentState)

	// Acquire Lock
	lockAcquired, err := r.acquireLock(ctx, msr, governancev1alpha1.MSRActionStateMSRRulesFulfillment, "Checking rules fulfillment")
	if !lockAcquired || err != nil {
		r.logger.V(2).Info("Lock already held, requeuing", "rulesFulfillmentState", msr.Status.RulesFulfillmentState)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	// Check repository connection exists
	if _, err := r.repositoryWithError(ctx, msr); err != nil {
		r.logger.Error(err, "Repository unavailable for spec reconciliation")
		_ = r.releaseLockWithFailure(ctx, msr, governancev1alpha1.MSRActionStateMSRRulesFulfillment, fmt.Errorf("check repository: %w", err))
		return ctrl.Result{}, err
	}

	// Dispatch to the correct reconcile sub-state handler
	// Each handler manages its own RulesFulfillmentState transition and lock release
	r.logger.V(2).Info("Dispatching to rules fulfillment sub-state handler", "rulesFulfillmentState", msr.Status.RulesFulfillmentState)
	switch msr.Status.RulesFulfillmentState {
	case governancev1alpha1.MSRRulesFulfillmentStateEmpty, governancev1alpha1.MSRRulesFulfillmentStateCheckSignatures:
		return r.handleMSRRulesFulfillmentStateCheckSignatures(ctx, msr)
	case governancev1alpha1.MSRRulesFulfillmentStateUpdateMCASpec:
		return r.handleMSRRulesFulfillmentStateUpdateMCASpec(ctx, msr)
	case governancev1alpha1.MSRRulesFulfillmentStateNotifyGovernors:
		return r.handleMSRRulesFulfillmentStateNotifyGovernors(ctx, msr)
	case governancev1alpha1.MSRRulesFulfillmentStateAbort:
		msr.Status.RulesFulfillmentState = governancev1alpha1.MSRRulesFulfillmentStateEmpty
		_ = r.releaseLockAndSetNextState(ctx, msr, governancev1alpha1.MSRActionStateEmpty)
		return ctrl.Result{}, err
	default:
		err := fmt.Errorf("unknown RulesFulfillmentState: %s", string(msr.Status.RulesFulfillmentState))
		r.logger.Error(err, "Invalid rulesFulfillment state")
		_ = r.releaseLockWithFailure(ctx, msr, governancev1alpha1.MSRActionStateEmpty, err)
		return ctrl.Result{}, err
	}
}

// handleMSRRulesFulfillmentStateCheckSignatures fetches MSR information from repository and verifies signatures.
// Sub-state: MSRRulesFulfillmentStateCheckSignatures → MSRRulesFulfillmentStateUpdateMCASpec (if fulfilled)
func (r *ManifestSigningRequestReconciler) handleMSRRulesFulfillmentStateCheckSignatures(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	return r.withRulesFulfillmentLock(ctx, msr, governancev1alpha1.MSRActionStateMSRRulesFulfillment, governancev1alpha1.MSRActionStateMSRRulesFulfillment,
		func(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (governancev1alpha1.MSRRulesFulfillmentState, error) {
			r.logger.Info("Checking MSR signature and rules fulfillment", "version", msr.Spec.Version)

			repo := r.repository(ctx, msr)

			// Check, that it's new commit hash
			remoteHead, err := repo.GetRemoteHeadCommit(ctx)
			if err != nil {
				r.logger.Error(err, "Failed to get remote repository HEAD")
				return "", fmt.Errorf("get remote repository HEAD: %w", err)
			}
			if msr.Status.LastObservedCommitHash == remoteHead {
				return governancev1alpha1.MSRRulesFulfillmentStateAbort, nil
			}

			// Fetch MSR from repository for the current version
			repoMSR, msrBytes, msrSig, govSigs, err := repo.FetchMSRByVersion(ctx, msr)
			if err != nil {
				r.logger.Error(err, "Failed to fetch MSR from repository", "version", msr.Spec.Version)
				return "", fmt.Errorf("fetch MSR from repository: %w", err)
			} else if repoMSR.Spec.PublicKey != msr.Spec.PublicKey {
				r.logger.Error(err, "Repository and in-cluster MSR public keys are different")
				return governancev1alpha1.MSRRulesFulfillmentStateAbort, nil
			}

			// Verify MSR signature against in-cluster MSR
			if _, err := commonvalidation.VerifySignature(msr.Spec.PublicKey, msrBytes, msrSig); err != nil {
				r.logger.Error(err, "MSR signature verification failed", "version", msr.Spec.Version)
				return "", fmt.Errorf("MSR signature verification failed: %w", err)
			}

			// Get verified signers from governor signatures
			verifiedSigners, _ := validationmsr.GetVerifiedSigners(repoMSR, govSigs, msrBytes)

			// Evaluate rules
			if !validation.EvaluateRules(repoMSR.Spec.Require, verifiedSigners) {
				// Rules not fulfilled yet - abort the MSRRulesFulfillmentState
				r.logger.Info("Rules not fulfilled yet, waiting for more signatures", "version", msr.Spec.Version)
				return governancev1alpha1.MSRRulesFulfillmentStateAbort, nil
			}

			// Rules fulfilled. Collect fulfillment signatures
			r.logger.Info("Updating MSR status to Approved", "version", msr.Spec.Version)

			collectedSigs := r.extractCollectedSignatures(verifiedSigners)
			msr.Status.CollectedSignatures = collectedSigs
			msr.Status.Status = governancev1alpha1.Approved
			msr.Status.LastObservedCommitHash = remoteHead

			// Update the latest RequestHistory record with collected signatures
			if len(msr.Status.RequestHistory) > 0 {
				latestIdx := len(msr.Status.RequestHistory) - 1
				msr.Status.RequestHistory[latestIdx].CollectedSignatures = collectedSigs
				msr.Status.RequestHistory[latestIdx].Status = governancev1alpha1.Approved
			}

			if err := r.Status().Update(ctx, msr); err != nil {
				r.logger.Error(err, "Failed to update MSR status to Approved", "version", msr.Spec.Version)
				return "", fmt.Errorf("update MSR status: %w", err)
			}

			r.logger.Info("Rules fulfilled, transitioning to update MSR state", "version", msr.Spec.Version, "signaturesCount", len(collectedSigs))
			return governancev1alpha1.MSRRulesFulfillmentStateUpdateMCASpec, nil
		},
	)
}

// extractCollectedSignatures extracts signatures that contributed to rule fulfillment.
func (r *ManifestSigningRequestReconciler) extractCollectedSignatures(
	verifiedSigners map[string]validation.SignatureStatus,
) []governancev1alpha1.Signature {
	signatures := []governancev1alpha1.Signature{}
	for alias, status := range verifiedSigners {
		if status != validation.Signed {
			continue
		}

		signatures = append(signatures, governancev1alpha1.Signature{Signer: alias})
	}

	return signatures
}

// handleMSRRulesFulfillmentStateUpdateMCASpec updates the MCA spec with fulfillment information.
// Sub-state: MSRRulesFulfillmentStateUpdateMCASpec → MSRRulesFulfillmentStateNotifyGovernors
func (r *ManifestSigningRequestReconciler) handleMSRRulesFulfillmentStateUpdateMCASpec(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	return r.withRulesFulfillmentLock(ctx, msr, governancev1alpha1.MSRActionStateMSRRulesFulfillment, governancev1alpha1.MSRActionStateMSRRulesFulfillment,
		func(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (governancev1alpha1.MSRRulesFulfillmentState, error) {
			r.logger.Info("Updating MCA spec with fulfillment information", "version", msr.Spec.Version)

			// Get MRT to find associated MCA
			mrt, err := r.getExistingMRTForMSR(ctx, msr)
			if err != nil {
				r.logger.Error(err, "Failed to get MRT for MSR", "msr", msr.Name)
				return "", fmt.Errorf("get MRT: %w", err)
			}

			// Fetch the MCA
			mca := &governancev1alpha1.ManifestChangeApproval{}
			mcaKey := types.NamespacedName{Name: mrt.Spec.MCA.Name, Namespace: mrt.Spec.MCA.Namespace}
			if err := r.Get(ctx, mcaKey, mca); err != nil {
				r.logger.Error(err, "Failed to get MCA", "mca", mcaKey)
				return "", fmt.Errorf("get MCA: %w", err)
			}

			// Update MCA spec with MSR fulfillment information
			mca.Spec.Version = msr.Spec.Version
			mca.Spec.CommitSHA = msr.Spec.CommitSHA
			mca.Spec.PreviousCommitSHA = msr.Spec.PreviousCommitSHA
			mca.Spec.PublicKey = msr.Spec.PublicKey
			mca.Spec.GitRepository = msr.Spec.GitRepository
			mca.Spec.Locations = msr.Spec.Locations
			mca.Spec.Changes = msr.Spec.Changes
			mca.Spec.Governors = msr.Spec.Governors
			mca.Spec.Require = msr.Spec.Require
			mca.Spec.CollectedSignatures = msr.Status.CollectedSignatures

			// Update MCA in cluster
			if err := r.Update(ctx, mca); err != nil {
				r.logger.Error(err, "Failed to update MCA with fulfillment information", "mca", mcaKey)
				return "", fmt.Errorf("update MCA: %w", err)
			}

			r.logger.Info("MCA updated with fulfillment information, transitioning to notify governors", "version", msr.Spec.Version)
			return governancev1alpha1.MSRRulesFulfillmentStateNotifyGovernors, nil
		},
	)
}

// handleMSRRulesFulfillmentStateNotifyGovernors sends notifications to governors about rule fulfillment.
// Sub-state: MSRRulesFulfillmentStateNotifyGovernors → MSRRulesFulfillmentStateEmpty
// Transitions ActionState back to MSRActionStateEmpty, completing the processing cycle.
func (r *ManifestSigningRequestReconciler) handleMSRRulesFulfillmentStateNotifyGovernors(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	return r.withRulesFulfillmentLock(ctx, msr, governancev1alpha1.MSRActionStateMSRRulesFulfillment, governancev1alpha1.MSRActionStateEmpty,
		func(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (governancev1alpha1.MSRRulesFulfillmentState, error) {
			r.logger.Info("Notifying governors about rules fulfillment", "version", msr.Spec.Version)

			if err := r.Notifier.NotifyGovernorsMSR(ctx, msr); err != nil {
				r.logger.Error(err, "Failed to notify governors", "version", msr.Spec.Version)
				return "", fmt.Errorf("notify governors: %w", err)
			}

			r.logger.Info("Governors notified, rules fulfillment complete", "version", msr.Spec.Version)
			return governancev1alpha1.MSRRulesFulfillmentStateEmpty, nil
		},
	)
}

// withLock wraps state handler logic with lock acquisition and release.
// It provides a clean abstraction for ActionState transitions:
// 1. Acquires lock with re-fetch to prevent conflicts
// 2. Executes the handler function
// 3. On success: releases lock and transitions to nextState
// 4. On failure: releases lock with failure reason and returns error
// This eliminates boilerplate and ensures consistent lock management.
func (r *ManifestSigningRequestReconciler) withLock(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
	state governancev1alpha1.MSRActionState,
	message string,
	handler MSRStateHandler,
) (ctrl.Result, error) {
	lockAcquired, err := r.acquireLock(ctx, msr, state, message)
	if !lockAcquired || err != nil {
		r.logger.V(2).Info("lock was already acquired, requeuing", "state", state)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	nextState, err := handler(ctx, msr)
	if err != nil {
		r.logger.Error(err, "Handler failed, releasing lock with failure", "state", state)
		_ = r.releaseLockWithFailure(ctx, msr, state, err)
		return ctrl.Result{}, err
	}

	r.logger.V(2).Info("Handler succeeded, transitioning state", "from", state, "to", nextState)
	releaseErr := r.releaseLockAndSetNextState(ctx, msr, nextState)
	return ctrl.Result{Requeue: true}, releaseErr
}

// withReconcileLock wraps reconcile sub-state handler logic.
// Assumes the outer lock (ActionState) is already held by the parent handler.
// It provides a clean abstraction for ReconcileState transitions:
// 1. Executes the handler function
// 2. On success: updates ReconcileState, releases lock with nextActionState, and requeues
// 3. On failure: releases lock with failure reason and returns error
// The nextActionState parameter allows specifying what ActionState to transition to,
// enabling the last handler to transition back to Empty.
func (r *ManifestSigningRequestReconciler) withReconcileLock(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
	parentState governancev1alpha1.MSRActionState,
	nextActionState governancev1alpha1.MSRActionState,
	handler MSRReconcileStateHandler,
) (ctrl.Result, error) {
	nextReconcileState, err := handler(ctx, msr)
	if err != nil {
		r.logger.Error(err, "Reconcile handler failed, releasing lock", "parentState", parentState, "currentReconcileState", msr.Status.ReconcileState)
		_ = r.releaseLockWithFailure(ctx, msr, parentState, err)
		return ctrl.Result{}, err
	}

	// Update ReconcileState
	r.logger.V(2).Info("Updating ReconcileState", "from", msr.Status.ReconcileState, "to", nextReconcileState)
	msr.Status.ReconcileState = nextReconcileState
	if err := r.Status().Update(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to update ReconcileState", "newState", nextReconcileState)
		_ = r.releaseLockWithFailure(ctx, msr, parentState, fmt.Errorf("update ReconcileState: %w", err))
		return ctrl.Result{}, err
	}

	// Transition ActionState to nextActionState and requeue
	releaseErr := r.releaseLockAndSetNextState(ctx, msr, nextActionState)
	if releaseErr == nil {
		r.logger.V(2).Info("Reconcile sub-state completed, requeuing", "nextReconcileState", nextReconcileState, "nextActionState", nextActionState)
	}
	return ctrl.Result{Requeue: true}, releaseErr
}

// withRulesFulfillmentLock wraps rules fulfillment sub-state handler logic.
// Assumes the outer lock (ActionState) is already held by the parent handler.
// It provides a clean abstraction for RulesFulfillmentState transitions:
// 1. Executes the handler function
// 2. On success: updates RulesFulfillmentState, releases lock with nextActionState, and requeues
// 3. On failure: releases lock with failure reason and returns error
// The nextActionState parameter allows specifying what ActionState to transition to,
// enabling the last handler to transition back to Empty.
func (r *ManifestSigningRequestReconciler) withRulesFulfillmentLock(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
	parentState governancev1alpha1.MSRActionState,
	nextActionState governancev1alpha1.MSRActionState,
	handler MSRRulesFulfillmentStateHandler,
) (ctrl.Result, error) {
	nextRulesFulfillmentState, err := handler(ctx, msr)
	if err != nil {
		r.logger.Error(err, "Rules fulfillment handler failed, releasing lock", "parentState", parentState, "currentRulesFulfillmentState", msr.Status.RulesFulfillmentState)
		_ = r.releaseLockWithFailure(ctx, msr, parentState, err)
		return ctrl.Result{}, err
	}

	// Update RulesFulfillmentState
	r.logger.V(2).Info("Updating RulesFulfillmentState", "from", msr.Status.RulesFulfillmentState, "to", nextRulesFulfillmentState)
	msr.Status.RulesFulfillmentState = nextRulesFulfillmentState
	if err := r.Status().Update(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to update RulesFulfillmentState", "newState", nextRulesFulfillmentState)
		_ = r.releaseLockWithFailure(ctx, msr, parentState, fmt.Errorf("update RulesFulfillmentState: %w", err))
		return ctrl.Result{}, err
	}

	// Transition ActionState to nextActionState and requeue
	releaseErr := r.releaseLockAndSetNextState(ctx, msr, nextActionState)
	if releaseErr == nil {
		r.logger.V(2).Info("Rules fulfillment sub-state completed, requeuing", "nextRulesFulfillmentState", nextRulesFulfillmentState, "nextActionState", nextActionState)
	}
	return ctrl.Result{Requeue: true}, releaseErr
}

// acquireLock attempts to acquire an exclusive lock for the given state.
// It re-fetches the MSR to avoid "object modified" conflicts.
// Returns (true, nil) if lock was acquired, (false, nil) if already held, (false, err) on error.
func (r *ManifestSigningRequestReconciler) acquireLock(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
	newState governancev1alpha1.MSRActionState,
	message string,
) (bool, error) {
	// Re-fetch is crucial to avoid "object modified" errors
	if err := r.Get(ctx, client.ObjectKeyFromObject(msr), msr); err != nil {
		r.logger.Error(err, "Failed to re-fetch MSR for lock acquisition", "state", newState)
		return false, fmt.Errorf("fetch fresh ManifestSigningRequest: %w", err)
	}

	// Check if Condition.Progressing was already set for this state
	if msr.Status.ActionState == newState && meta.IsStatusConditionTrue(msr.Status.Conditions, governancev1alpha1.Progressing) {
		r.logger.V(3).Info("Lock already held for this state", "state", newState)
		return false, nil
	}

	// Set ActionState and Progressing condition
	msr.Status.ActionState = newState
	meta.SetStatusCondition(&msr.Status.Conditions, metav1.Condition{
		Type: governancev1alpha1.Progressing, Status: metav1.ConditionTrue, Reason: string(newState), Message: message,
	})

	// Save the lock
	if err := r.Status().Update(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to acquire lock", "state", newState)
		return false, fmt.Errorf("update ManifestSigningRequest after lock acquired: %w", err)
	}

	r.logger.V(2).Info("Lock acquired", "state", newState, "message", message)
	return true, nil
}

// releaseLockWithFailure releases the lock and sets the reason to StepFailed.
// It preserves the ActionState for retry.
func (r *ManifestSigningRequestReconciler) releaseLockWithFailure(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
	nextState governancev1alpha1.MSRActionState,
	cause error,
) error {
	r.logger.V(2).Info("Releasing lock due to failure", "state", nextState, "error", cause.Error())
	return r.releaseLockAbstract(ctx, msr, nextState, "StepFailed", fmt.Sprintf("Error occurred: %v", cause))
}

// releaseLockAndSetNextState releases the lock and transitions to the next state.
// It sets the reason to StepComplete indicating successful completion.
func (r *ManifestSigningRequestReconciler) releaseLockAndSetNextState(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
	nextState governancev1alpha1.MSRActionState,
) error {
	r.logger.V(2).Info("Releasing lock and transitioning state", "nextState", nextState)
	return r.releaseLockAbstract(ctx, msr, nextState, "StepComplete", "Step completed, proceeding to next state")
}

// releaseLockAbstract is the internal implementation for lock release.
// It updates the Progressing condition to False and transitions ActionState.
func (r *ManifestSigningRequestReconciler) releaseLockAbstract(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
	nextState governancev1alpha1.MSRActionState,
	reason, message string,
) error {
	// The passed 'msr' should already be the latest version
	msr.Status.ActionState = nextState
	meta.SetStatusCondition(&msr.Status.Conditions, metav1.Condition{
		Type: governancev1alpha1.Progressing, Status: metav1.ConditionFalse, Reason: reason, Message: message,
	})

	if err := r.Status().Update(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to release lock", "nextState", nextState, "reason", reason)
		return err
	}

	r.logger.V(3).Info("Lock released", "nextState", nextState, "reason", reason)
	return nil
}

// getExistingMRTForMSR retrieves the ManifestRequestTemplate referenced by the MSR.
// Returns an error if the MRT cannot be found.
func (r *ManifestSigningRequestReconciler) getExistingMRTForMSR(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (*governancev1alpha1.ManifestRequestTemplate, error) {
	mrtKey := types.NamespacedName{
		Name:      msr.Spec.MRT.Name,
		Namespace: msr.Spec.MRT.Namespace,
	}

	mrt := &governancev1alpha1.ManifestRequestTemplate{}
	if err := r.Get(ctx, mrtKey, mrt); err != nil {
		r.logger.V(2).Info("ManifestRequestTemplate not found", "mrt", mrtKey)
		return nil, fmt.Errorf("fetch ManifestRequestTemplate for ManifestSigningRequest: %w", err)
	}

	return mrt, nil
}

// repositoryWithError retrieves the Git repository provider for the MSR's MRT.
// Returns an error if the MRT cannot be found or no provider is available.
func (r *ManifestSigningRequestReconciler) repositoryWithError(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (repomanager.GitRepository, error) {
	mrt, err := r.getExistingMRTForMSR(ctx, msr)
	if err != nil {
		return nil, fmt.Errorf("get ManifestRequestTemplate for repository: %w", err)
	}

	return r.RepoManager.GetProviderForMRT(ctx, mrt)
}

// repository retrieves the Git repository provider for the MSR.
// This version ignores errors and is used where the MRT is guaranteed to exist.
func (r *ManifestSigningRequestReconciler) repository(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) repomanager.GitRepository {
	mrt, _ := r.getExistingMRTForMSR(ctx, msr)
	repo, _ := r.RepoManager.GetProviderForMRT(ctx, mrt)
	return repo
}
