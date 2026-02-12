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
	"strings"
	"time"

	argocdv1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
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

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	"github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/notifier"
	repomanager "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository"
)

const (
	ApplySummaryFileName  = "apply-summary.yaml"
	ApplySummaryHeader    = "# Summary of NEW and MODIFIED resources"
	DeleteSummaryFileName = "delete-summary.yaml"
	DeleteSummaryHeader   = "# Summary of DELETED resources"
)

// MCAStateHandler defines a function that performs work within a state and returns the next state
type MCAStateHandler func(ctx context.Context, msr *governancev1alpha1.ManifestChangeApproval) (governancev1alpha1.MCAActionState, error)

// MCAReconcileStateHandler defines a function that performs work within a reconcile state
// and returns the next reconcile state
type MCAReconcileStateHandler func(ctx context.Context, msr *governancev1alpha1.ManifestChangeApproval) (governancev1alpha1.MCAReconcileNewMCASpecState, error)

// ManifestChangeApprovalReconciler reconciles a ManifestChangeApproval object
type ManifestChangeApprovalReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	RepoManager     RepositoryManager
	NotifierManager *notifier.Manager
	logger          logr.Logger
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManifestChangeApprovalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&governancev1alpha1.ManifestChangeApproval{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Named("manifestchangeapproval").
		Complete(r)
}

// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestchangeapprovals,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestchangeapprovals/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestchangeapprovals/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile orchestrates the reconciliation flow for ManifestSigningRequest.
// It handles three main paths:
// 1. Deletion: Remove finalizer and cleanup
// 2. Initialization: Create, push to Git, update history, set finalizer
// 3. Normal: Process new versions
func (r *ManifestChangeApprovalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = logf.FromContext(ctx).WithValues("controller", "ManifestChangeApproval", "name", req.Name, "namespace", req.Namespace)
	r.logger.Info("Reconciling ManifestChangeApproval")

	// Fetch the MCA instance
	mca := &governancev1alpha1.ManifestChangeApproval{}
	if err := r.Get(ctx, req.NamespacedName, mca); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if !mca.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, mca)
	}

	// Release lock if hold too long (deadlock prevention)
	if r.isLockForMoreThan(mca, 30*time.Second) {
		r.logger.Info("Lock held too long, releasing to prevent deadlock", "actionState", mca.Status.ActionState, "lockDuration", "30s")
		return ctrl.Result{Requeue: true}, r.releaseLockWithFailure(ctx, mca, mca.Status.ActionState, fmt.Errorf("lock acquired for too long"))
	}

	// Handle initialization
	if !controllerutil.ContainsFinalizer(mca, GovernanceFinalizer) {
		return r.reconcileCreate(ctx, mca, req)
	}

	// Handle normal reconciliation (after initialization)
	r.logger.Info("MCA initialized, processing normal reconciliation", "actionState", mca.Status.ActionState)
	return r.reconcileNormal(ctx, mca, req)
}

// reconcileCreate handles the logic for a newly created MCA that has not been initialized.
func (r *ManifestChangeApprovalReconciler) reconcileDelete(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(mca, GovernanceFinalizer) {
		// No custom finalizer is found. Do nothing
		return ctrl.Result{}, nil
	}

	// No real clean-up logic is needed
	r.logger.Info("Successfully finalized ManifestChangeApproval")

	// Remove the custom finalizer. The object will be deleted
	controllerutil.RemoveFinalizer(mca, GovernanceFinalizer)
	if err := r.Update(ctx, mca); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer %s: %w", GovernanceFinalizer, err)
	}

	return ctrl.Result{}, nil
}

// isLockForMoreThan checks if the Progressing lock has been held for longer than the specified duration.
// Used for deadlock detection and prevention.
func (r *ManifestChangeApprovalReconciler) isLockForMoreThan(
	mca *governancev1alpha1.ManifestChangeApproval,
	duration time.Duration,
) bool {
	condition := meta.FindStatusCondition(mca.Status.Conditions, governancev1alpha1.Progressing)
	return condition != nil && condition.Status == metav1.ConditionTrue && time.Now().Sub(condition.LastTransitionTime.Time) >= duration
}

// reconcileCreate handles initialization of a new MCA.
// It progresses through these states:
// 1. MCAActionStateGitPushMCA: Push initial MCA manifest to Git
// 2. MCAActionStateUpdateAfterGitPush: Update in-cluster history after push
// 3. MCAActionStateUpdateArgoCD: Update ArgoCD Application targetRevision
// 4. MCAActionStateInitSetFinalizer: Set finalizer to complete initialization
func (r *ManifestChangeApprovalReconciler) reconcileCreate(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
	req ctrl.Request,
) (ctrl.Result, error) {
	r.logger.Info("Initializing new MCA", "actionState", mca.Status.ActionState)

	// Check if there is any available git repository provider
	if _, err := r.repositoryWithError(ctx, mca); err != nil {
		r.logger.Error(err, "Repository provider unavailable, cannot proceed with initialization")
		return ctrl.Result{}, fmt.Errorf("init repo for ManifestChangeApproval: %w", err)
	}

	switch mca.Status.ActionState {
	case governancev1alpha1.MCAActionStateEmpty, governancev1alpha1.MCAActionStateGitPushMCA:
		// 1. Create an initial MCA file in governance folder in Git repository.
		r.logger.Info("Pushing initial MCA to Git repository")
		return r.handleStateGitCommit(ctx, mca)
	case governancev1alpha1.MCAActionStatePushSummaryFiles:
		// 2. Create and push summary files (apply and delete manifests).
		r.logger.Info("Pushing summary files to repository")
		return r.handleStatePushSummaryFiles(ctx, mca)
	case governancev1alpha1.MCAActionStateUpdateAfterGitPush:
		// 3. Update information in-cluster MCA (add history record) after Git repository push.
		r.logger.Info("Updating MCA after initial Git push")
		return r.handleUpdateAfterGitPush(ctx, mca)
	case governancev1alpha1.MCAActionStateUpdateArgoCD:
		// 4. Update ArgoCD Application targetRevision.
		r.logger.Info("Updating ArgoCD Application target revision")
		return r.handleStateUpdateArgoCD(ctx, mca)
	case governancev1alpha1.MCAActionStateInitSetFinalizer:
		// 5. Confirm MCA initialization by setting the GovernanceFinalizer.
		r.logger.Info("Setting finalizer to complete initialization")
		return r.handleStateFinalizer(ctx, mca)
	default:
		err := fmt.Errorf("unknown initialization state: %s", string(mca.Status.ActionState))
		r.logger.Error(err, "Invalid state for initialization")
		return ctrl.Result{}, err
	}
}

// handleStateGitCommit pushes the initial MCA manifest to the Git repository.
// State: MCAActionStateGitPushMCA → MCAActionStatePushSummaryFiles
func (r *ManifestChangeApprovalReconciler) handleStateGitCommit(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) (ctrl.Result, error) {
	return r.withLock(ctx, mca, governancev1alpha1.MCAActionStateGitPushMCA, "Pushing MCA manifest to Git repository",
		func(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) (governancev1alpha1.MCAActionState, error) {
			r.logger.Info("Saving initial MCA to Git repository", "mca", mca.Name, "namespace", mca.Namespace)
			if err := r.saveInRepository(ctx, mca); err != nil {
				r.logger.Error(err, "Failed to save MCA to repository")
				return "", fmt.Errorf("save MCA in Git repository: %w", err)
			}
			r.logger.Info("MCA saved to repository successfully")
			return governancev1alpha1.MCAActionStatePushSummaryFiles, nil
		},
	)
}

// handleStatePushSummaryFiles creates and pushes summary files (apply and delete manifests).
// State: MCAActionStatePushSummaryFiles → MCAActionStateUpdateAfterGitPush
func (r *ManifestChangeApprovalReconciler) handleStatePushSummaryFiles(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) (ctrl.Result, error) {
	return r.withLock(ctx, mca, governancev1alpha1.MCAActionStatePushSummaryFiles, "Creating and pushing summary files to repository",
		func(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) (governancev1alpha1.MCAActionState, error) {
			r.logger.Info("Creating and pushing summary files", "mca", mca.Name, "version", mca.Spec.Version)
			if err := r.saveSummaryChanges(ctx, mca); err != nil {
				r.logger.Error(err, "Failed to save summary changes")
				return "", fmt.Errorf("save summary changes: %w", err)
			}
			r.logger.Info("Summary files pushed to repository successfully")
			return governancev1alpha1.MCAActionStateUpdateAfterGitPush, nil
		},
	)
}

// handleUpdateAfterGitPush updates the in-cluster MCA status with history after Git push.
// State: MCAActionStateUpdateAfterGitPush → MCAActionStateUpdateArgoCD
func (r *ManifestChangeApprovalReconciler) handleUpdateAfterGitPush(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) (ctrl.Result, error) {
	return r.withLock(ctx, mca, governancev1alpha1.MCAActionStateUpdateAfterGitPush, "Updating in-cluster MCA information after Git push",
		func(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) (governancev1alpha1.MCAActionState, error) {
			r.logger.Info("Adding history record after Git push", "version", mca.Spec.Version, "newHistoryCount", len(mca.Status.ApprovalHistory)+1)
			newRecord := r.createNewMCAHistoryRecordFromSpec(mca)
			mca.Status.ApprovalHistory = append(mca.Status.ApprovalHistory, newRecord)
			if err := r.Status().Update(ctx, mca); err != nil {
				r.logger.Error(err, "Failed to update MCA with history record", "version", newRecord.Version)
				return "", fmt.Errorf("update MCA information after Git push: %w", err)
			}
			r.logger.Info("MCA history updated successfully")
			return governancev1alpha1.MCAActionStateUpdateArgoCD, nil
		},
	)
}

// handleStateUpdateArgoCD updates the ArgoCD Application's targetRevision to the approved Commit SHA.
// State: MCAActionStateUpdateArgoCD → MCAActionStateInitSetFinalizer
func (r *ManifestChangeApprovalReconciler) handleStateUpdateArgoCD(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) (ctrl.Result, error) {
	return r.withLock(ctx, mca, governancev1alpha1.MCAActionStateUpdateArgoCD, "Updating ArgoCD Application target revision",
		func(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) (governancev1alpha1.MCAActionState, error) {
			// Get the Application
			app, err := r.getApplicationForMCA(ctx, mca)
			if err != nil {
				return "", fmt.Errorf("fetch Application for MCA: %w", err)
			}

			// Patch the Application with MCA CommitSHA
			r.logger.Info("Patching ArgoCD Application targetRevision", "app", app.Name, "oldRevision", mca.Spec.PreviousCommitSHA, "newRevision", mca.Spec.CommitSHA)

			patch := client.MergeFrom(app.DeepCopy())
			app.Spec.Source.TargetRevision = mca.Spec.CommitSHA

			if err := r.Patch(ctx, app, patch); err != nil {
				r.logger.Error(err, "Failed to patch ArgoCD Application")
				return "", fmt.Errorf("patch ArgoCD Application targetRevision: %w", err)
			}

			r.logger.Info("ArgoCD Application updated successfully")
			return governancev1alpha1.MCAActionStateInitSetFinalizer, nil
		},
	)
}

// handleStateFinalizer sets the GovernanceFinalizer on the MCA to complete initialization.
// State: MCAActionStateInitSetFinalizer → MCAActionStateEmpty
func (r *ManifestChangeApprovalReconciler) handleStateFinalizer(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) (ctrl.Result, error) {
	return r.withLock(ctx, mca, governancev1alpha1.MCAActionStateInitSetFinalizer, "Setting the GovernanceFinalizer on MCA",
		func(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) (governancev1alpha1.MCAActionState, error) {
			r.logger.Info("Adding GovernanceFinalizer to complete initialization")
			controllerutil.AddFinalizer(mca, GovernanceFinalizer)
			if err := r.Update(ctx, mca); err != nil {
				r.logger.Error(err, "Failed to add finalizer")
				return "", fmt.Errorf("add finalizer in initialization: %w", err)
			}
			r.logger.Info("Initialization complete, finalizer added")
			return governancev1alpha1.MCAActionStateEmpty, nil
		},
	)
}

// reconcileNormal handles reconciliation for initialized MCA resources.
// It processes new versions detected in the Spec and orchestrates the reconciliation state machine.
// If Spec.Version > latest history version, transitions to MCAReconcileNewMCASpec state.
func (r *ManifestChangeApprovalReconciler) reconcileNormal(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
	req ctrl.Request,
) (ctrl.Result, error) {
	// Check for active lock
	if meta.IsStatusConditionTrue(mca.Status.Conditions, governancev1alpha1.Progressing) {
		r.logger.V(2).Info("Waiting for ongoing operation to complete", "actionState", mca.Status.ActionState)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// If ActionState is empty, we need to decide what action to do
	if mca.Status.ActionState == governancev1alpha1.MCAActionStateEmpty {
		// Get the latest history record version
		latestHistoryVersion := -1
		history := mca.Status.ApprovalHistory
		if len(history) > 0 {
			latestHistoryVersion = history[len(history)-1].Version
		}

		// If the spec's version is newer - reconcile status
		if mca.Spec.Version > latestHistoryVersion {
			r.logger.Info("New version detected in spec, starting reconciliation", "specVersion", mca.Spec.Version, "latestVersion", latestHistoryVersion)
			return ctrl.Result{Requeue: true}, r.releaseLockAndSetNextState(ctx, mca, governancev1alpha1.MCAActionStateNewMCASpec)
		}

		// No new version detected
		r.logger.V(3).Info("No new version to process", "specVersion", mca.Spec.Version, "latestVersion", latestHistoryVersion)
	}

	// Execute action based on current ActionState
	switch mca.Status.ActionState {
	case governancev1alpha1.MCAActionStateNewMCASpec:
		r.logger.V(2).Info("Processing new MCA spec")
		return r.handleReconcileNewMCASpec(ctx, mca)
	default:
		// If the state is unknown - reset to EmptyState
		r.logger.V(2).Info("Unknown action state, resetting", "actionState", mca.Status.ActionState)
		return ctrl.Result{}, r.releaseLockAndSetNextState(ctx, mca, governancev1alpha1.MCAActionStateEmpty)
	}
}

// handleReconcileNewMCASpec orchestrates processing of a new MCA Spec version.
// State: MCAReconcileNewMCASpec with sub-states:
// 1. MCAReconcileNewMCASpecStateGitPushMCA: Push updated MCA to Git
// 2. MCAReconcileNewMCASpecStateUpdateAfterGitPush: Add history record
// 3. MCAReconcileNewMCASpecStateUpdateArgoCD: Update ArgoCD Application targetRevision
// 4. MCAReconcileRStateNotifyGovernors: Notify governors
// Final state: MCAEmptyActionState
func (r *ManifestChangeApprovalReconciler) handleReconcileNewMCASpec(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) (ctrl.Result, error) {
	r.logger.Info("Starting reconciliation of new MCA spec", "version", mca.Spec.Version, "reconcileState", mca.Status.ReconcileState)

	// Acquire Lock
	lockAcquired, err := r.acquireLock(ctx, mca, governancev1alpha1.MCAActionStateNewMCASpec, "Processing new MCA Spec")
	if !lockAcquired || err != nil {
		r.logger.V(2).Info("Lock already held, requeuing", "reconcileState", mca.Status.ReconcileState)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	// Check repository connection exists
	if _, err := r.repositoryWithError(ctx, mca); err != nil {
		r.logger.Error(err, "Repository unavailable for spec reconciliation")
		_ = r.releaseLockWithFailure(ctx, mca, governancev1alpha1.MCAActionStateNewMCASpec, fmt.Errorf("check repository: %w", err))
		return ctrl.Result{}, err
	}

	// Dispatch to the correct reconcile sub-state handler
	// Each handler manages its own ReconcileState transition and lock release
	r.logger.V(2).Info("Dispatching to reconcile sub-state handler", "reconcileState", mca.Status.ReconcileState)
	switch mca.Status.ReconcileState {
	case governancev1alpha1.MCAReconcileNewMCASpecStateEmpty, governancev1alpha1.MCAReconcileNewMCASpecStateGitPushMCA:
		return r.handleMCAReconcileStateGitCommit(ctx, mca)
	case governancev1alpha1.MCAReconcileNewMCASpecStatePushSummaryFiles:
		return r.handleMCAReconcileStatePushSummaryFiles(ctx, mca)
	case governancev1alpha1.MCAReconcileNewMCASpecStateUpdateAfterGitPush:
		return r.handleMCAReconcileStateUpdateAfterGitPush(ctx, mca)
	case governancev1alpha1.MCAReconcileNewMCASpecStateUpdateArgoCD:
		return r.handleReconcileStateUpdateArgoCD(ctx, mca)
	case governancev1alpha1.MCAReconcileNewMCASpecStateNotifyGovernors:
		return r.handleReconcileStateNotifyGovernors(ctx, mca)
	default:
		err := fmt.Errorf("unknown ReconcileState: %s", string(mca.Status.ReconcileState))
		r.logger.Error(err, "Invalid reconcile state")
		_ = r.releaseLockWithFailure(ctx, mca, governancev1alpha1.MCAActionStateNewMCASpec, err)
		return ctrl.Result{}, err
	}
}

// handleMCAReconcileStateGitCommit pushes the updated MCA manifest to Git repository.
// Sub-state: MCAReconcileNewMCASpecStateGitPushMCA → MCAReconcileNewMCASpecStatePushSummaryFiles
func (r *ManifestChangeApprovalReconciler) handleMCAReconcileStateGitCommit(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) (ctrl.Result, error) {
	return r.withReconcileLock(ctx, mca, governancev1alpha1.MCAActionStateNewMCASpec, governancev1alpha1.MCAActionStateNewMCASpec,
		func(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) (governancev1alpha1.MCAReconcileNewMCASpecState, error) {
			r.logger.Info("Pushing updated MCA manifest to Git repository", "version", mca.Spec.Version)
			if err := r.saveInRepository(ctx, mca); err != nil {
				r.logger.Error(err, "Failed to push MCA to repository", "version", mca.Spec.Version)
				return "", fmt.Errorf("save MCA in Git repository: %w", err)
			}
			r.logger.Info("MCA pushed to repository, transitioning to push summary files", "version", mca.Spec.Version)
			return governancev1alpha1.MCAReconcileNewMCASpecStatePushSummaryFiles, nil
		},
	)
}

// handleMCAReconcileStatePushSummaryFiles creates and pushes summary files (apply and delete manifests).
// Sub-state: MCAReconcileNewMCASpecStatePushSummaryFiles → MCAReconcileNewMCASpecStateUpdateAfterGitPush
func (r *ManifestChangeApprovalReconciler) handleMCAReconcileStatePushSummaryFiles(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) (ctrl.Result, error) {
	return r.withReconcileLock(ctx, mca, governancev1alpha1.MCAActionStateNewMCASpec, governancev1alpha1.MCAActionStateNewMCASpec,
		func(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) (governancev1alpha1.MCAReconcileNewMCASpecState, error) {
			r.logger.Info("Creating and pushing summary files", "version", mca.Spec.Version)
			if err := r.saveSummaryChanges(ctx, mca); err != nil {
				r.logger.Error(err, "Failed to save summary changes", "version", mca.Spec.Version)
				return "", fmt.Errorf("save summary changes: %w", err)
			}
			r.logger.Info("Summary files pushed to repository, transitioning to update history", "version", mca.Spec.Version)
			return governancev1alpha1.MCAReconcileNewMCASpecStateUpdateAfterGitPush, nil
		},
	)
}

// handleMCAReconcileStateUpdateAfterGitPush adds a history record after Git push.
// Sub-state: MCAReconcileNewMCASpecStateUpdateAfterGitPush → MCAReconcileNewMCASpecStateUpdateArgoCD
func (r *ManifestChangeApprovalReconciler) handleMCAReconcileStateUpdateAfterGitPush(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) (ctrl.Result, error) {
	return r.withReconcileLock(ctx, mca, governancev1alpha1.MCAActionStateNewMCASpec, governancev1alpha1.MCAActionStateNewMCASpec,
		func(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) (governancev1alpha1.MCAReconcileNewMCASpecState, error) {
			r.logger.Info("Adding reconciliation history record", "version", mca.Spec.Version)
			newRecord := r.createNewMCAHistoryRecordFromSpec(mca)
			mca.Status.ApprovalHistory = append(mca.Status.ApprovalHistory, newRecord)
			if err := r.Status().Update(ctx, mca); err != nil {
				r.logger.Error(err, "Failed to add history record", "version", newRecord.Version)
				return "", fmt.Errorf("update MCA information after Git push: %w", err)
			}
			r.logger.Info("History record added, ready to notify governors", "version", newRecord.Version, "newHistoryCount", len(mca.Status.ApprovalHistory))
			return governancev1alpha1.MCAReconcileNewMCASpecStateUpdateArgoCD, nil
		},
	)
}

// handleReconcileStateUpdateArgoCD updates the ArgoCD Application's targetRevision to the approved Commit SHA.
// Sub-state: MCAReconcileNewMCASpecStateUpdateArgoCD → MCAReconcileNewMCASpecStateNotifyGovernors
func (r *ManifestChangeApprovalReconciler) handleReconcileStateUpdateArgoCD(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) (ctrl.Result, error) {
	return r.withReconcileLock(ctx, mca, governancev1alpha1.MCAActionStateNewMCASpec, governancev1alpha1.MCAActionStateNewMCASpec,
		func(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) (governancev1alpha1.MCAReconcileNewMCASpecState, error) {
			// Get the Application
			app, err := r.getApplicationForMCA(ctx, mca)
			if err != nil {
				return "", fmt.Errorf("fetch Application for MCA: %w", err)
			}

			// Patch the Application with MCA CommitSHA
			r.logger.Info("Patching ArgoCD Application targetRevision", "app", app.Name, "oldRevision", mca.Spec.PreviousCommitSHA, "newRevision", mca.Spec.CommitSHA)

			patch := client.MergeFrom(app.DeepCopy())
			app.Spec.Source.TargetRevision = mca.Spec.CommitSHA

			if err := r.Patch(ctx, app, patch); err != nil {
				r.logger.Error(err, "Failed to patch ArgoCD Application")
				return "", fmt.Errorf("patch ArgoCD Application targetRevision: %w", err)
			}

			r.logger.Info("ArgoCD Application updated successfully")
			return governancev1alpha1.MCAReconcileNewMCASpecStateNotifyGovernors, nil
		},
	)
}

// handleReconcileStateNotifyGovernors sends notifications to governors about new approval.
// Sub-state: MCAReconcileNewMCASpecStateNotifyGovernors → MCAReconcileNewMCASpecStateEmpty
func (r *ManifestChangeApprovalReconciler) handleReconcileStateNotifyGovernors(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) (ctrl.Result, error) {
	return r.withReconcileLock(ctx, mca, governancev1alpha1.MCAActionStateNewMCASpec, governancev1alpha1.MCAActionStateEmpty,
		func(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) (governancev1alpha1.MCAReconcileNewMCASpecState, error) {
			r.logger.Info("Sending notifications to governors", "version", mca.Spec.Version, "requireSignatures", mca.Spec.Require)
			if err := r.NotifierManager.NotifyGovernorsMCA(ctx, mca); err != nil {
				r.logger.Error(err, "Failed to send notifications to governors", "version", mca.Spec.Version)
				return "", fmt.Errorf("send notification to governors: %w", err)
			}
			r.logger.Info("Notifications sent successfully, spec reconciliation complete", "version", mca.Spec.Version)
			return governancev1alpha1.MCAReconcileNewMCASpecStateEmpty, nil
		},
	)
}

func (r *ManifestChangeApprovalReconciler) saveInRepository(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) error {
	repositoryMCA := r.createRepositoryMCA(mca)
	_, err := withGitRepository(ctx, defaultGitOperationTimeout,
		func(repoCtx context.Context) (repomanager.GitRepository, error) {
			mrt, err := r.getExistingMRTForMCA(repoCtx, mca)
			if err != nil {
				return nil, err
			}
			return r.RepoManager.GetProviderForMRT(repoCtx, mrt)
		},
		func(repoCtx context.Context, repo repomanager.GitRepository) (string, error) {
			return repo.PushMCA(repoCtx, &repositoryMCA)
		},
	)
	if err != nil {
		r.logger.Error(err, "Failed to push initial ManifestChangeApproval in repository")
		return fmt.Errorf("push initial ManifestChangeApproval to repository: %w", err)
	}

	return nil
}

func (r *ManifestChangeApprovalReconciler) saveSummaryChanges(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) error {
	type changedFilesResult struct {
		fileChanges []governancev1alpha1.FileChange
		content     map[string]string
	}
	res, err := withGitRepository(ctx, defaultGitOperationTimeout,
		func(repoCtx context.Context) (repomanager.GitRepository, error) {
			mrt, err := r.getExistingMRTForMCA(repoCtx, mca)
			if err != nil {
				return nil, err
			}
			return r.RepoManager.GetProviderForMRT(repoCtx, mrt)
		},
		func(repoCtx context.Context, repo repomanager.GitRepository) (changedFilesResult, error) {
			fileChanges, content, err := repo.GetChangedFiles(repoCtx, mca.Spec.PreviousCommitSHA, mca.Spec.CommitSHA, mca.Spec.Locations.SourcePath)
			if err != nil {
				return changedFilesResult{}, err
			}
			return changedFilesResult{fileChanges: fileChanges, content: content}, nil
		},
	)
	if err != nil {
		r.logger.Error(err, "Failed to get changed files from repository")
		return fmt.Errorf("get changed files: %w", err)
	}
	fileChanges := res.fileChanges
	content := res.content

	applyContent := r.createApplyManifest(fileChanges, content)
	if applyContent != "" {
		_, err := withGitRepository(ctx, defaultGitOperationTimeout,
			func(repoCtx context.Context) (repomanager.GitRepository, error) {
				mrt, err := r.getExistingMRTForMCA(repoCtx, mca)
				if err != nil {
					return nil, err
				}
				return r.RepoManager.GetProviderForMRT(repoCtx, mrt)
			},
			func(repoCtx context.Context, repo repomanager.GitRepository) (string, error) {
				return repo.PushSummaryFile(repoCtx, applyContent, ApplySummaryFileName, mca.Spec.Locations.GovernancePath, mca.Spec.Version)
			},
		)
		if err != nil {
			return fmt.Errorf("push apply summary manifest: %w", err)
		}
	}
	deleteContent := r.createDeleteManifest(fileChanges, content)
	if deleteContent != "" {
		_, err := withGitRepository(ctx, defaultGitOperationTimeout,
			func(repoCtx context.Context) (repomanager.GitRepository, error) {
				mrt, err := r.getExistingMRTForMCA(repoCtx, mca)
				if err != nil {
					return nil, err
				}
				return r.RepoManager.GetProviderForMRT(repoCtx, mrt)
			},
			func(repoCtx context.Context, repo repomanager.GitRepository) (string, error) {
				return repo.PushSummaryFile(repoCtx, deleteContent, DeleteSummaryFileName, mca.Spec.Locations.GovernancePath, mca.Spec.Version)
			},
		)
		if err != nil {
			return fmt.Errorf("push delete summary manifest: %w", err)
		}
	}

	return nil
}

func (r *ManifestChangeApprovalReconciler) createApplyManifest(
	fileChanges []governancev1alpha1.FileChange,
	content map[string]string,
) string {
	var manifests []string

	for _, f := range fileChanges {
		if f.Status == governancev1alpha1.Deleted {
			continue
		}

		manifests = append(manifests, content[f.Path])
	}

	if len(manifests) == 0 {
		return ""
	}

	return ApplySummaryHeader + "\n" + strings.Join(manifests, "---\n")
}

func (r *ManifestChangeApprovalReconciler) createDeleteManifest(
	fileChanges []governancev1alpha1.FileChange,
	content map[string]string,
) string {
	var manifests []string

	for _, f := range fileChanges {
		if f.Status != governancev1alpha1.Deleted {
			continue
		}

		manifests = append(manifests, content[f.Path])
	}

	if len(manifests) == 0 {
		return ""
	}

	return DeleteSummaryHeader + "\n" + strings.Join(manifests, "---\n")
}

func (r *ManifestChangeApprovalReconciler) createRepositoryMCA(
	mca *governancev1alpha1.ManifestChangeApproval,
) governancev1alpha1.ManifestChangeApprovalManifestObject {
	mcaCopy := mca.DeepCopy()
	return governancev1alpha1.ManifestChangeApprovalManifestObject{
		TypeMeta: metav1.TypeMeta{
			Kind:       mcaCopy.Kind,
			APIVersion: mcaCopy.APIVersion,
		},
		ObjectMeta: governancev1alpha1.ManifestRef{
			Name:      mcaCopy.Name,
			Namespace: mcaCopy.Namespace,
		},
		Spec: mcaCopy.Spec,
	}
}

func (r *ManifestChangeApprovalReconciler) saveNewHistoryRecord(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) error {
	// Append a new history record
	newRecord := r.createNewMCAHistoryRecordFromSpec(mca)
	mca.Status.ApprovalHistory = append(mca.Status.ApprovalHistory, newRecord)

	// Save the change
	if err := r.Status().Update(ctx, mca); err != nil {
		r.logger.Error(err, "Failed to update MCA status with new history record")
		return fmt.Errorf("update ManifestChangeApproval status with new history record: %w", err)
	}

	r.logger.Info("Successfully added new MCA record to status")
	return nil
}

func (r *ManifestChangeApprovalReconciler) createNewMCAHistoryRecordFromSpec(
	mca *governancev1alpha1.ManifestChangeApproval,
) governancev1alpha1.ManifestChangeApprovalHistoryRecord {
	mcaCopy := mca.DeepCopy()

	return governancev1alpha1.ManifestChangeApprovalHistoryRecord{
		CommitSHA:           mcaCopy.Spec.CommitSHA,
		PreviousCommitSHA:   mcaCopy.Spec.PreviousCommitSHA,
		Time:                metav1.NewTime(time.Now()),
		Version:             mcaCopy.Spec.Version,
		Changes:             mcaCopy.Spec.Changes,
		Governors:           mcaCopy.Spec.Governors,
		Require:             mcaCopy.Spec.Require,
		CollectedSignatures: mcaCopy.Spec.CollectedSignatures,
	}
}

// withLock wraps state handler logic with lock acquisition and release.
// It provides a clean abstraction for ActionState transitions:
// 1. Acquires lock with re-fetch to prevent conflicts
// 2. Executes the handler function
// 3. On success: releases lock and transitions to nextState
// 4. On failure: releases lock with failure reason and returns error
// This eliminates boilerplate and ensures consistent lock management.
func (r *ManifestChangeApprovalReconciler) withLock(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
	state governancev1alpha1.MCAActionState,
	message string,
	handler MCAStateHandler,
) (ctrl.Result, error) {
	lockAcquired, err := r.acquireLock(ctx, mca, state, message)
	if !lockAcquired || err != nil {
		r.logger.V(2).Info("lock was already acquired, requeuing", "state", state)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	nextState, err := handler(ctx, mca)
	if err != nil {
		r.logger.Error(err, "Handler failed, releasing lock with failure", "state", state)
		_ = r.releaseLockWithFailure(ctx, mca, state, err)
		return ctrl.Result{}, err
	}

	r.logger.V(2).Info("Handler succeeded, transitioning state", "from", state, "to", nextState)
	releaseErr := r.releaseLockAndSetNextState(ctx, mca, nextState)
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
func (r *ManifestChangeApprovalReconciler) withReconcileLock(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
	parentState governancev1alpha1.MCAActionState,
	nextActionState governancev1alpha1.MCAActionState,
	handler MCAReconcileStateHandler,
) (ctrl.Result, error) {
	nextReconcileState, err := handler(ctx, mca)
	if err != nil {
		r.logger.Error(err, "Reconcile handler failed, releasing lock", "parentState", parentState, "currentReconcileState", mca.Status.ReconcileState)
		_ = r.releaseLockWithFailure(ctx, mca, parentState, err)
		return ctrl.Result{}, err
	}

	// Update ReconcileState
	r.logger.V(2).Info("Updating ReconcileState", "from", mca.Status.ReconcileState, "to", nextReconcileState)
	mca.Status.ReconcileState = nextReconcileState
	if err := r.Status().Update(ctx, mca); err != nil {
		r.logger.Error(err, "Failed to update ReconcileState", "newState", nextReconcileState)
		_ = r.releaseLockWithFailure(ctx, mca, parentState, fmt.Errorf("update ReconcileState: %w", err))
		return ctrl.Result{}, err
	}

	// Transition ActionState to nextActionState and requeue
	releaseErr := r.releaseLockAndSetNextState(ctx, mca, nextActionState)
	if releaseErr == nil {
		r.logger.V(2).Info("Reconcile sub-state completed, requeuing", "nextReconcileState", nextReconcileState, "nextActionState", nextActionState)
	}
	return ctrl.Result{Requeue: true}, releaseErr
}

// acquireLock attempts to acquire an exclusive lock for the given state.
// It re-fetches the MCA to avoid "object modified" conflicts.
// Returns (true, nil) if lock was acquired, (false, nil) if already held, (false, err) on error.
func (r *ManifestChangeApprovalReconciler) acquireLock(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
	newState governancev1alpha1.MCAActionState,
	message string,
) (bool, error) {
	// Re-fetch is crucial to avoid "object modified" errors
	if err := r.Get(ctx, client.ObjectKeyFromObject(mca), mca); err != nil {
		r.logger.Error(err, "Failed to re-fetch MCA for lock acquisition", "state", newState)
		return false, fmt.Errorf("fetch fresh ManifestChangeApproval: %w", err)
	}

	// Check if Condition.Progressing was already set for this state
	if mca.Status.ActionState == newState && meta.IsStatusConditionTrue(mca.Status.Conditions, governancev1alpha1.Progressing) {
		r.logger.V(3).Info("Lock already held for this state", "state", newState)
		return false, nil
	}

	// Set ActionState and Progressing condition
	mca.Status.ActionState = newState
	meta.SetStatusCondition(&mca.Status.Conditions, metav1.Condition{
		Type: governancev1alpha1.Progressing, Status: metav1.ConditionTrue, Reason: string(newState), Message: message,
	})

	// Save the lock
	if err := r.Status().Update(ctx, mca); err != nil {
		r.logger.Error(err, "Failed to acquire lock", "state", newState)
		return false, fmt.Errorf("update ManifestChangeApproval after lock acquired: %w", err)
	}

	r.logger.V(2).Info("Lock acquired", "state", newState, "message", message)
	return true, nil
}

// releaseLockWithFailure releases the lock and sets the reason to StepFailed.
// It preserves the ActionState for retry.
func (r *ManifestChangeApprovalReconciler) releaseLockWithFailure(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
	nextState governancev1alpha1.MCAActionState,
	cause error,
) error {
	r.logger.V(2).Info("Releasing lock due to failure", "state", nextState, "error", cause.Error())
	return r.releaseLockAbstract(ctx, mca, nextState, "StepFailed", fmt.Sprintf("Error occurred: %v", cause))
}

// releaseLockAndSetNextState releases the lock and transitions to the next state.
// It sets the reason to StepComplete indicating successful completion.
func (r *ManifestChangeApprovalReconciler) releaseLockAndSetNextState(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
	nextState governancev1alpha1.MCAActionState,
) error {
	r.logger.V(2).Info("Releasing lock and transitioning state", "nextState", nextState)
	return r.releaseLockAbstract(ctx, mca, nextState, "StepComplete", "Step completed, proceeding to next state")
}

// releaseLockAbstract is the internal implementation for lock release.
// It updates the Progressing condition to False and transitions ActionState.
func (r *ManifestChangeApprovalReconciler) releaseLockAbstract(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
	nextState governancev1alpha1.MCAActionState,
	reason, message string,
) error {
	// The passed 'mca' should already be the latest version
	mca.Status.ActionState = nextState
	meta.SetStatusCondition(&mca.Status.Conditions, metav1.Condition{
		Type: governancev1alpha1.Progressing, Status: metav1.ConditionFalse, Reason: reason, Message: message,
	})

	if err := r.Status().Update(ctx, mca); err != nil {
		r.logger.Error(err, "Failed to release lock", "nextState", nextState, "reason", reason)
		return err
	}

	r.logger.V(3).Info("Lock released", "nextState", nextState, "reason", reason)
	return nil
}

func (r *ManifestChangeApprovalReconciler) getApplicationForMCA(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) (*argocdv1alpha1.Application, error) {
	mrt, err := r.getExistingMRTForMCA(ctx, mca)
	if err != nil {
		return nil, fmt.Errorf("fetch ManifestRequestTemplate to use it for Application fetch: %w", err)
	}

	appKey := types.NamespacedName{
		Name:      mrt.Spec.ArgoCD.Application.Name,
		Namespace: mrt.Spec.ArgoCD.Application.Namespace,
	}

	app := &argocdv1alpha1.Application{}
	if err := r.Get(ctx, appKey, app); err != nil {
		return nil, fmt.Errorf("fetch Application for ManifestChangeApproval: %w", err)
	}

	return app, nil
}

func (r *ManifestChangeApprovalReconciler) getExistingMRTForMCA(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) (*governancev1alpha1.ManifestRequestTemplate, error) {
	mrtKey := types.NamespacedName{
		Name:      mca.Spec.MRT.Name,
		Namespace: mca.Spec.MRT.Namespace,
	}

	mrt := &governancev1alpha1.ManifestRequestTemplate{}
	if err := r.Get(ctx, mrtKey, mrt); err != nil {
		return nil, fmt.Errorf("fetch ManifestRequestTemplate for ManifestChangeApproval: %w", err)
	}

	return mrt, nil
}

func (r *ManifestChangeApprovalReconciler) repositoryWithError(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) (repomanager.GitRepository, error) {
	mrt, err := r.getExistingMRTForMCA(ctx, mca)
	if err != nil {
		return nil, fmt.Errorf("get ManifestRequestTemplate for repository: %w", err)
	}

	return r.RepoManager.GetProviderForMRT(ctx, mrt)
}

func (r *ManifestChangeApprovalReconciler) repository(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) repomanager.GitRepository {
	mrt, _ := r.getExistingMRTForMCA(ctx, mca)

	gitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	repo, _ := r.RepoManager.GetProviderForMRT(gitCtx, mrt)
	return repo
}
