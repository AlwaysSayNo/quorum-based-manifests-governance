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

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	repomanager "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository"
)

type Notifier interface {
	NotifyGovernors(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) error
}

// ManifestSigningRequestReconciler reconciles a ManifestSigningRequest object
type ManifestSigningRequestReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	RepoManager RepositoryManager
	Notifier    Notifier
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

func (r *ManifestSigningRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = logf.FromContext(ctx).WithValues("controller", "ManifestSigningRequest", "name", req.Name, "namespace", req.Namespace)
	r.logger.Info("Reconciling ManifestSigningRequest")

	// Fetch the MSR instance
	msr := &governancev1alpha1.ManifestSigningRequest{}
	if err := r.Get(ctx, req.NamespacedName, msr); err != nil {
		r.logger.Info("DEBUG: MSR not found")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if !msr.ObjectMeta.DeletionTimestamp.IsZero() {
		r.logger.Info("DEBUG: MSR reconcileDelete")
		return r.reconcileDelete(ctx, msr)
	}

	// Release lock if hold too long
	if r.isLockForMoreThan(msr, 30*time.Second) {
		r.logger.Info("DEBUG: MSR removeProgressing")
		return ctrl.Result{Requeue: true}, r.releaseLockWithFailure(ctx, msr, msr.Status.ActionState, fmt.Errorf("lock acquired for too long"))
	}

	// Handle initialization
	if !controllerutil.ContainsFinalizer(msr, GovernanceFinalizer) {
		r.logger.Info("DEBUG: MSR reconcileCreate")
		return r.reconcileCreate(ctx, msr, req)
	}

	// Handle normal reconciliation
	r.logger.Info("DEBUG: MSR reconcileNormal")
	return r.reconcileNormal(ctx, msr, req)
}

// reconcileDelete handles the cleanup logic when an MSR is being deleted.
func (r *ManifestSigningRequestReconciler) reconcileDelete(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(msr, GovernanceFinalizer) {
		// No custom finalizer is found. Do nothing
		return ctrl.Result{}, nil
	}

	// No real clean-up logic is needed
	r.logger.Info("Finalizing ManifestSigningRequest", "currentState", msr.Status.ActionState)

	switch msr.Status.ActionState {
	default:
		// Any state means we need to start the deletion process.
		return r.handleStateDeletion(ctx, msr)
	}
}

// handleStateDeletion removes entry for current MSR from index file and finalizer from this resource.
func (r *ManifestSigningRequestReconciler) handleStateDeletion(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	// Skip lock acquiring.

	// Remove the finalizer from MSR.
	r.logger.Info("Start removing finalizer.")
	controllerutil.RemoveFinalizer(msr, GovernanceFinalizer)
	if err := r.Update(ctx, msr); err != nil {
		_ = r.releaseLockWithFailure(ctx, msr, governancev1alpha1.MSRStateDeletionInProgress, err)
		return ctrl.Result{}, fmt.Errorf("remove finalizer from MSRStateDeletionInProgress: %w", err)
	}
	r.logger.Info("Finish removing finalizer.")

	r.logger.Info("Successfully deleted ManifestSigningRequest.")
	err := r.releaseLockAndSetNextState(ctx, msr, governancev1alpha1.MSREmptyActionState)
	return ctrl.Result{}, err
}

func (r *ManifestSigningRequestReconciler) isLockForMoreThan(
	msr *governancev1alpha1.ManifestSigningRequest,
	duration time.Duration,
) bool {
	condition := meta.FindStatusCondition(msr.Status.Conditions, governancev1alpha1.Progressing)
	return condition != nil && condition.Status == metav1.ConditionTrue && time.Now().Sub(condition.LastTransitionTime.Time) >= duration
}

// reconcileCreate handles the logic for a newly created MSR that has not been initialized.
func (r *ManifestSigningRequestReconciler) reconcileCreate(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
	req ctrl.Request,
) (ctrl.Result, error) {
	r.logger.Info("Reconciling new ManifestSigningRequest", "currentState", msr.Status.ActionState)

	// Check, if there is any available git repository provider
	if _, err := r.repositoryWithError(ctx, msr); err != nil {
		r.logger.Error(err, "Failed on first repository fetch")
		return ctrl.Result{}, fmt.Errorf("init repo for ManifestSigningRequest: %w", err)
	}

	switch msr.Status.ActionState {
	case governancev1alpha1.MSREmptyActionState, governancev1alpha1.MSRStateGitPushMSR:
		// 1. Create an initial MSR file in governance folder in Git repository.
		r.logger.Info("handleStateGitCommit")
		return r.handleStateGitCommit(ctx, msr)
	case governancev1alpha1.MSRStateUpdateAfterGitPush:
		// 2. Update information in-cluster MST (add history record) after Git repository push.
		r.logger.Info("handleUpdateAfterGitPush")
		return r.handleUpdateAfterGitPush(ctx, msr)
	case governancev1alpha1.MSRStateInitSetFinalizer:
		// 3. Confirm MSR initialization, by setting the GovernanceFinalizer.
		r.logger.Info("handleStateFinalizer")
		return r.handleStateFinalizer(ctx, msr)
	default:
		// Any unknown ActionState in MSR during initialization - error.
		r.logger.Info(fmt.Sprintf("Unknown initialization state: %s", string(msr.Status.ActionState)))
		return ctrl.Result{}, fmt.Errorf("unknown initialization state: %s", string(msr.Status.ActionState))
	}
}

// handleStateGitCommit is responsible for managing lock and pushing MSR manifest to Git repository.
func (r *ManifestSigningRequestReconciler) handleStateGitCommit(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	// Acquire Lock
	if lockAcquired, err := r.acquireLock(ctx, msr, governancev1alpha1.MSRStateGitPushMSR, "Pushing MSR manifest to Git repository"); !lockAcquired || err != nil {
		r.logger.Info("lock was already acquired")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	// Save MSR in Git repository.
	r.logger.Info("Start saving MSR in Git repository")
	if err := r.saveInRepository(ctx, msr); err != nil {
		err = fmt.Errorf("save MSR in Git repository: %w", err)
		_ = r.releaseLockWithFailure(ctx, msr, governancev1alpha1.MSRStateGitPushMSR, err)
		return ctrl.Result{}, err
	}
	r.logger.Info("Finish saving initial MSR in Git repository")

	err := r.releaseLockAndSetNextState(ctx, msr, governancev1alpha1.MSRStateUpdateAfterGitPush)
	if err != nil {
		r.logger.Info("DEBUG: Unsuccessfull handleStateGitCommit")
	} else {
		r.logger.Info("DEBUG: Successfull handleStateGitCommit")
	}
	return ctrl.Result{Requeue: true}, err
}

// handleUpdateAfterGitPush is responsible for managing lock and updating in-cluster MSR information after Git push.
func (r *ManifestSigningRequestReconciler) handleUpdateAfterGitPush(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	// Acquire Lock
	if lockAcquired, err := r.acquireLock(ctx, msr, governancev1alpha1.MSRStateUpdateAfterGitPush, "Updating in-cluster MSR information after Git push"); !lockAcquired || err != nil {
		r.logger.Info("lock was already acquired")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	// Update MSR information after Git push.
	r.logger.Info("Start updating MSR after Git repository push")
	// Add new initial history record
	newRecord := r.createNewMSRHistoryRecordFromSpec(msr)
	msr.Status.RequestHistory = append(msr.Status.RequestHistory, newRecord)
	if err := r.Status().Update(ctx, msr); err != nil {
		err = fmt.Errorf("update MSR information after Git push: %w", err)
		_ = r.releaseLockWithFailure(ctx, msr, governancev1alpha1.MSRStateUpdateAfterGitPush, err)
		return ctrl.Result{}, err
	}
	r.logger.Info("Finish updating MSR after Git repository push")

	err := r.releaseLockAndSetNextState(ctx, msr, governancev1alpha1.MSRStateInitSetFinalizer)
	if err != nil {
		r.logger.Info("DEBUG: Unsuccessfull handleUpdateAfterGitPush")
	} else {
		r.logger.Info("DEBUG: Successfull handleUpdateAfterGitPush")
	}
	return ctrl.Result{Requeue: true}, err
}

// handleStateFinalizer is responsible for managing lock and setting the GovernanceFinalizer on MSR.
func (r *ManifestSigningRequestReconciler) handleStateFinalizer(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	// Acquire Lock
	if lockAcquired, err := r.acquireLock(ctx, msr, governancev1alpha1.MSRStateInitSetFinalizer, "Setting the GovernanceFinalizer on MSR"); !lockAcquired || err != nil {
		r.logger.Info("lock was already acquired")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	// Set the GovernanceFinalizer on MSR.
	r.logger.Info("Start setting the GovernanceFinalizer on MSR")

	controllerutil.AddFinalizer(msr, GovernanceFinalizer)
	if err := r.Update(ctx, msr); err != nil {
		err = fmt.Errorf("add finalizer in initialization: %w", err)
		_ = r.releaseLockWithFailure(ctx, msr, governancev1alpha1.MSRStateInitSetFinalizer, err)
		return ctrl.Result{}, err
	}

	r.logger.Info("Finish setting the GovernanceFinalizer on MSR")

	err := r.releaseLockAndSetNextState(ctx, msr, governancev1alpha1.MSREmptyActionState)
	if err != nil {
		r.logger.Info("DEBUG: Unsuccessfull handleStateFinalizer")
	} else {
		r.logger.Info("DEBUG: Successfull handleStateFinalizer")
	}
	return ctrl.Result{Requeue: true}, err
}

func (r *ManifestSigningRequestReconciler) reconcileNormal(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
	req ctrl.Request,
) (ctrl.Result, error) {
	// Check an active Lock.
	if meta.IsStatusConditionTrue(msr.Status.Conditions, governancev1alpha1.Progressing) {
		r.logger.Info("Waiting for ongoing operation to complete.", "operation", msr.Status.ActionState)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// If ActionState is empty, we need to decide what action to do.
	if msr.Status.ActionState == governancev1alpha1.MSREmptyActionState {
		// Get the latest history record version
		latestHistoryVersion := -1
		history := msr.Status.RequestHistory
		if len(history) > 0 {
			latestHistoryVersion = history[len(history)-1].Version
		}

		// If the spec's version is newer - reconcile status
		if msr.Spec.Version > latestHistoryVersion {
			r.logger.Info("Detected new version in spec. Transitioning to MSRReconcileNewMSRSpec state.", "specVersion", msr.Spec.Version, "statusVersion", latestHistoryVersion)
			return ctrl.Result{Requeue: true}, r.releaseLockAndSetNextState(ctx, msr, governancev1alpha1.MSRReconcileNewMSRSpec)
		}
	}

	// TODO: Signature Observer logic
	// Execute action.
	switch msr.Status.ActionState {
	case governancev1alpha1.MSRReconcileNewMSRSpec:
		return r.handleReconcileNewMSRSpec(ctx, msr)
	default:
		// If the state is unknown - set to EmptyState.
		return ctrl.Result{}, r.releaseLockAndSetNextState(ctx, msr, governancev1alpha1.MSREmptyActionState)
	}
}

// handleReconcileNewMSRSpec is responsible for managing lock and processing new MSR Spec.
func (r *ManifestSigningRequestReconciler) handleReconcileNewMSRSpec(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	// Acquire Lock.
	if lockAcquired, err := r.acquireLock(ctx, msr, governancev1alpha1.MSRReconcileNewMSRSpec, "Processing new MSR Spec"); !lockAcquired || err != nil {
		r.logger.Info("lock was already acquired")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	// Check repository connection exists.
	if _, err := r.repositoryWithError(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to connect to repository.")
		err = fmt.Errorf("check repository: %w", err)
		_ = r.releaseLockWithFailure(ctx, msr, governancev1alpha1.MSRReconcileNewMSRSpec, err)
		return ctrl.Result{}, err
	}

	// Dispatch to the correct revision sub-state handler.
	var result ctrl.Result
	var err error

	// In case of failure, let the main handler release the lock.
	switch msr.Status.ReconcileState {
	case governancev1alpha1.MSRReconcileStateRevisionEmpty, governancev1alpha1.MSRReconcileStateGitPushMSR:
		// 1.
		r.logger.Info("DEBUG: handleReconcileNewMSRSpec#handleMSRReconcileStateGitCommit")
		result, err = r.handleMSRReconcileStateGitCommit(ctx, msr)
	case governancev1alpha1.MSRReconcileRStateUpdateAfterGitPush:
		// 2. Update the MSR Spec with data from new revision (changed files, revision hash, etc.).
		r.logger.Info("DEBUG: handleReconcileNewMSRSpec#handleSubStateUpdateMSR")
		result, err = r.handleMSRReconcileStateUpdateAfterGitPush(ctx, msr)
	case governancev1alpha1.MSRReconcileRStateNotifyGovernors:
		// 2. Update the MSR Spec with data from new revision (changed files, revision hash, etc.).
		r.logger.Info("DEBUG: handleReconcileNewMSRSpec#handleMSRReconcileStateNotifyGovernors")
		result, err = r.handleMSRReconcileStateNotifyGovernors(ctx, msr)
	default:
		// Any unknown ReconcileState during state processing - error.
		r.logger.Info(fmt.Sprintf("Unknown ReconcileState state: %s", string(msr.Status.ReconcileState)))
		return ctrl.Result{}, fmt.Errorf("unknown ReconcileState state: %s", string(msr.Status.ReconcileState))

	}

	// If any step failed, we release the lock but keep the meta-state.
	if err != nil {
		r.logger.Info("DEBUG: handleReconcileNewMSRSpec: error after sub state call")
		_ = r.releaseLockWithFailure(ctx, msr, governancev1alpha1.MSRReconcileNewMSRSpec, err)
		return result, err
	}

	r.logger.Info("DEBUG: handleReconcileNewMSRSpec: successfull sub state call")
	return result, nil
}

// handleMSRReconcileStateGitCommit is responsible for releasing lock and pushing MSR manifest to Git repository.
func (r *ManifestSigningRequestReconciler) handleMSRReconcileStateGitCommit(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {
	// Save MSR in Git repository.
	r.logger.Info("Start saving MSR in Git repository")
	if err := r.saveInRepository(ctx, msr); err != nil {
		err = fmt.Errorf("save MSR in Git repository: %w", err)
		return ctrl.Result{}, err
	}
	r.logger.Info("Finish saving initial MSR in Git repository")

	// Revision passes. Transition ReconcileState to MSRReconcileRStateUpdateAfterGitPush.
	msr.Status.ReconcileState = governancev1alpha1.MSRReconcileRStateUpdateAfterGitPush
	if err := r.Status().Update(ctx, msr); err != nil {
		r.logger.Error(err, "Failure while updating ReconcileState")
		err = fmt.Errorf("update ReconcileState: %w", err)
		return ctrl.Result{}, err
	}

	err := r.releaseLockAndSetNextState(ctx, msr, governancev1alpha1.MSRReconcileNewMSRSpec)
	if err != nil {
		r.logger.Info("DEBUG: Unsuccessful handleMSRReconcileStateGitCommit")
	} else {
		r.logger.Info("DEBUG: Successful handleMSRReconcileStateGitCommit")
	}
	return ctrl.Result{Requeue: true}, err
}

// handleMSRReconcileStateUpdateAfterGitPush is responsible for releasing lock and updating in-cluster MSR information after Git push.
func (r *ManifestSigningRequestReconciler) handleMSRReconcileStateUpdateAfterGitPush(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {

	// Update MSR information after Git push.
	r.logger.Info("Start updating MSR after Git repository push")
	// Add new initial history record
	newRecord := r.createNewMSRHistoryRecordFromSpec(msr)
	msr.Status.RequestHistory = append(msr.Status.RequestHistory, newRecord)
	// Update MSRReconcile state
	msr.Status.ReconcileState = governancev1alpha1.MSRReconcileRStateNotifyGovernors

	if err := r.Status().Update(ctx, msr); err != nil {
		r.logger.Error(err, "Failure while updating ReconcileState")
		err = fmt.Errorf("update MSR information after Git push: %w", err)
		return ctrl.Result{}, err
	}
	r.logger.Info("Finish updating MSR after Git repository push")

	err := r.releaseLockAndSetNextState(ctx, msr, governancev1alpha1.MSRReconcileNewMSRSpec)
	if err != nil {
		r.logger.Info("DEBUG: Unsuccessfull handleMSRReconcileStateUpdateAfterGitPush")
	} else {
		r.logger.Info("DEBUG: Successfull handleMSRReconcileStateUpdateAfterGitPush")
	}
	return ctrl.Result{Requeue: true}, err
}

// handleMSRReconcileStateNotifyGovernors is responsible for releasing lock and sending notification to governors.
func (r *ManifestSigningRequestReconciler) handleMSRReconcileStateNotifyGovernors(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) (ctrl.Result, error) {

	// Send notification to governors.
	r.logger.Info("Start sending notification to governors")

	// Notify the Governors
	if err := r.Notifier.NotifyGovernors(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to send notifications to governors")
		err := fmt.Errorf("send notification to governors")
		return ctrl.Result{}, err
	}

	// Update MSRReconcile state
	msr.Status.ReconcileState = governancev1alpha1.MSRReconcileRStateNotifyGovernors
	if err := r.Status().Update(ctx, msr); err != nil {
		err = fmt.Errorf("update MSR information after Git push: %w", err)
		return ctrl.Result{}, err
	}
	r.logger.Info("Finish sending notification to governors")

	err := r.releaseLockAndSetNextState(ctx, msr, governancev1alpha1.MSREmptyActionState)
	if err != nil {
		r.logger.Info("DEBUG: Unsuccessfull handleMSRReconcileStateNotifyGovernors")
	} else {
		r.logger.Info("DEBUG: Successfull handleMSRReconcileStateNotifyGovernors")
	}
	return ctrl.Result{Requeue: true}, err
}

func (r *ManifestSigningRequestReconciler) saveInRepository(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) error {
	repositoryMSR := r.createRepositoryMSR(msr)
	if _, err := r.repository(ctx, msr).PushMSR(ctx, &repositoryMSR); err != nil {
		r.logger.Error(err, "Failed to push initial ManifestSigningRequest in repository")
		return fmt.Errorf("push initial ManifestSigningRequest to repository: %w", err)
	}

	return nil
}

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

func (r *ManifestSigningRequestReconciler) saveNewHistoryRecord(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) error {
	// Append a new history record
	newRecord := r.createNewMSRHistoryRecordFromSpec(msr)
	msr.Status.RequestHistory = append(msr.Status.RequestHistory, newRecord)

	// Save the change
	if err := r.Status().Update(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to update MSR status with new history record")
		return fmt.Errorf("update ManifestSigningRequest status with new history record: %w", err)
	}

	r.logger.Info("Successfully added new MSR record to status")
	return nil
}

func (r *ManifestSigningRequestReconciler) createNewMSRHistoryRecordFromSpec(
	msr *governancev1alpha1.ManifestSigningRequest,
) governancev1alpha1.ManifestSigningRequestHistoryRecord {
	msrCopy := msr.DeepCopy()

	return governancev1alpha1.ManifestSigningRequestHistoryRecord{
		Version:   msrCopy.Spec.Version,
		Changes:   msrCopy.Spec.Changes,
		Governors: msrCopy.Spec.Governors,
		Require:   msrCopy.Spec.Require,
		Approves:  msr.Status.Approves,
		Status:    msrCopy.Spec.Status,
	}
}

// acquireLock sets in cluster MSR ActionState and Condition.Progressing to True, if it isn't set yet.
func (r *ManifestSigningRequestReconciler) acquireLock(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
	newState governancev1alpha1.MSRActionState,
	message string,
) (bool, error) {
	// Re-fetch is crucial to avoid "object modified" errors
	if err := r.Get(ctx, client.ObjectKeyFromObject(msr), msr); err != nil {
		return false, fmt.Errorf("fetch fresh ManifestSigningRequest: %w", err)
	}

	// Check, if Condition.Progressing was already set.
	if msr.Status.ActionState == newState && meta.IsStatusConditionTrue(msr.Status.Conditions, governancev1alpha1.Progressing) {
		return false, nil
	}

	// Set ActionState.
	msr.Status.ActionState = newState
	meta.SetStatusCondition(&msr.Status.Conditions, metav1.Condition{
		Type: governancev1alpha1.Progressing, Status: metav1.ConditionTrue, Reason: string(newState), Message: message,
	})

	// Save new Status changes.
	if err := r.Status().Update(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to acquire lock", "state", newState)
		return false, fmt.Errorf("update ManifestSigningRequest after lock acquired: %w", err)
	}
	return true, nil
}

func (r *ManifestSigningRequestReconciler) releaseLockWithFailure(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
	nextState governancev1alpha1.MSRActionState,
	cause error,
) error {
	return r.releaseLockAbstract(ctx, msr, nextState, "StepFailed", fmt.Sprintf("Error occurred: %v", cause))
}

func (r *ManifestSigningRequestReconciler) releaseLockAndSetNextState(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
	nextState governancev1alpha1.MSRActionState,
) error {
	return r.releaseLockAbstract(ctx, msr, nextState, "StepComplete", "Step completed, proceeding to next state")
}

func (r *ManifestSigningRequestReconciler) releaseLockAbstract(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
	nextState governancev1alpha1.MSRActionState,
	reason, message string,
) error {
	// The passed 'msr' should already be the latest version. Don't do Get.

	msr.Status.ActionState = nextState
	meta.SetStatusCondition(&msr.Status.Conditions, metav1.Condition{
		Type: governancev1alpha1.Progressing, Status: metav1.ConditionFalse, Reason: reason, Message: message,
	})

	return r.Status().Update(ctx, msr)
}

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
		return nil, fmt.Errorf("fetch ManifestRequestTemplate for ManifestSigningRequest: %w", err)
	}

	return mrt, nil
}

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

func (r *ManifestSigningRequestReconciler) repository(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) repomanager.GitRepository {
	mrt, _ := r.getExistingMRTForMSR(ctx, msr)
	repo, _ := r.RepoManager.GetProviderForMRT(ctx, mrt)
	return repo
}
