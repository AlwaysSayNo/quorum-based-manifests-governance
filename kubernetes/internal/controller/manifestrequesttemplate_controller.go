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
	"path/filepath"
	"slices"
	"strings"
	"time"

	argocdv1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	repomanager "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository"
)

const (
	GovernanceFinalizer       = "governance.nazar.grynko.com/finalizer"
	QubmangoOperationalFolder = ".qubmango"
	QubmangoOperationalFile   = QubmangoOperationalFolder + "/index.yaml"
)

type RepositoryManager interface {
	GetProviderForMRT(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (repomanager.GitRepository, error)
}

// ManifestRequestTemplateReconciler reconciles a ManifestRequestTemplate object
type ManifestRequestTemplateReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	RepoManager RepositoryManager
	logger      logr.Logger
}

func Pointer[T any](d T) *T {
	return &d
}

// SetupWithManager sets up the controller with the Manager.
// This controller watches both ManifestRequestTemplate resources and ArgoCD Application resources.
// When an Application changes, it triggers reconciliation of the associated MRT.
func (r *ManifestRequestTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&governancev1alpha1.ManifestRequestTemplate{}).
		Named("manifestrequesttemplate").
		Complete(r)
}

// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestrequesttemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestrequesttemplates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestrequesttemplates/finalizers,verbs=update
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestsigningrequests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestchangeapprovals,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *ManifestRequestTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = log.FromContext(ctx).WithValues("controller", "ManifestRequestTemplate", "name", req.Name, "namespace", req.Namespace)

	r.logger.Info("Reconciling ManifestRequestTemplate")

	// Fetch the MRT instance
	mrt := &governancev1alpha1.ManifestRequestTemplate{}
	if err := r.Get(ctx, req.NamespacedName, mrt); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if !mrt.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, mrt)
	}

	// Handle initialization
	if !controllerutil.ContainsFinalizer(mrt, GovernanceFinalizer) {
		return r.reconcileCreate(ctx, mrt, req)
	}

	// Handle normal reconciliation
	return r.reconcileNormal(ctx, mrt, req)
}

// reconcileDelete handles the cleanup logic when an MRT is being deleted.
func (r *ManifestRequestTemplateReconciler) reconcileDelete(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	// Check if the finalizer is present.
	if !controllerutil.ContainsFinalizer(mrt, GovernanceFinalizer) {
		return ctrl.Result{}, nil
	}

	r.logger.Info("Finalizing ManifestRequestTemplate", "currentState", mrt.Status.ActionState)

	switch mrt.Status.ActionState {
	default:
		// Any state means we need to start the deletion process.
		return r.handleStateDeletion(ctx, mrt)
	}
}

func (r *ManifestRequestTemplateReconciler) handleStateDeletion(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	// Acquire Lock.
	if lockAcquired, err := r.acquireLock(ctx, mrt, governancev1alpha1.StateDeletionInProgress, "Removing entry from Git index"); !lockAcquired || err != nil {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}

	// Clean up the entry in the .qubmango/index.yaml file.
	r.logger.Info("Start removing entry from Git index file.")
	governanceIndexAlias := mrt.Namespace + ":" + mrt.Name
	if _, _, err := r.repository(ctx, mrt).RemoveFromIndexFile(ctx, QubmangoOperationalFile, governanceIndexAlias); err != nil {
		// The Git push failed. Release the lock and retry this step again.
		_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.StateDeletionInProgress, err)
		return ctrl.Result{}, fmt.Errorf("failed to remove entry from index file: %w", err)
	}
	r.logger.Info("Finish removing entry from Git index file.")

	// Remove the finalizer from MRT.
	r.logger.Info("Start removing finalizer.")
	controllerutil.RemoveFinalizer(mrt, GovernanceFinalizer)
	if err := r.Update(ctx, mrt); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer from ManifestRequestTemplate: %w", err)
	}
	r.logger.Info("Finish removing finalizer.")

	r.logger.Info("Successfully finalized ManifestRequestTemplate.")
	return ctrl.Result{}, nil
}

// reconcileCreate handles the logic for a newly created MRT, that has not been initialized.
func (r *ManifestRequestTemplateReconciler) reconcileCreate(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	req ctrl.Request,
) (ctrl.Result, error) {
	r.logger.Info("Reconciling new ManifestRequestTemplate", "currentState", mrt.Status.ActionState)

	// Check, if there is any available git repository provider
	if _, err := r.repositoryWithError(ctx, mrt); err != nil {
		r.logger.Error(err, "Failed on first repository fetch")
		return ctrl.Result{}, fmt.Errorf("init repo for ManifestRequestTemplate: %w", err)
	}

	switch mrt.Status.ActionState {
	case governancev1alpha1.EmptyActionState, governancev1alpha1.StateInitGitGovernanceInitialization:
		// 1. Create an entry in .qubmango/index.yaml file and save the commit as LastObservedCommitHash.
		return r.handleInitStateGitCommit(ctx, mrt)
	case governancev1alpha1.InitStateCreateDefaultClusterResources:
		// 2. Create default MSR, MCA resources in the cluster.
		return r.handleInitStateCreateClusterResources(ctx, mrt)
	case governancev1alpha1.StateInitSetFinalizer:
		// 3. Confirm MRT initialization, by setting the GovernanceFinalizer.
		return r.handleStateFinalizing(ctx, mrt)
	default:
		// Any unknown ActionState in MRT during initialization - error.
		r.logger.Info(fmt.Sprintf("Unknown initialization state: %s", string(mrt.Status.ActionState)))
		return ctrl.Result{}, fmt.Errorf("unknown initialization state: %s", string(mrt.Status.ActionState))
	}
}

// handleInitStateGitCommit is responsible for managing lock and creating an entry in the .qubmango/index.yaml file.
func (r *ManifestRequestTemplateReconciler) handleInitStateGitCommit(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	// Acquire Lock
	if lockAcquired, err := r.acquireLock(ctx, mrt, governancev1alpha1.StateInitGitGovernanceInitialization, "Pushing initial commit to Git"); !lockAcquired || err != nil {
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	// Create an entry in the index file
	governanceIndexAlias := mrt.Namespace + ":" + mrt.Name
	commitHash, isNewRecord, err := r.repository(ctx, mrt).InitializeGovernance(ctx, QubmangoOperationalFile, governanceIndexAlias, mrt.Spec.Location.Folder)

	if err != nil {
		err = fmt.Errorf("initialize governance in repository: %w", err)
		_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.StateInitGitGovernanceInitialization, err)
		return ctrl.Result{}, err
	} else if !isNewRecord {
		// Index already exists. commitHash variable is empty. Take the latest git commit hash from repository.
		commitHash, err = r.repository(ctx, mrt).GetLatestRevision(ctx)
		if err != nil {
			err = fmt.Errorf("take latest commit, when index already exists in repository: %w", err)
			_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.StateInitGitGovernanceInitialization, err)
			return ctrl.Result{}, err
		}
	}

	// Release lock on success and move to the next state.
	mrt.Status.LastObservedCommitHash = commitHash
	err = r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.InitStateCreateDefaultClusterResources)
	return ctrl.Result{Requeue: true}, err
}

// handleInitStateCreateClusterResources is responsible for managing lock and creating the default MSR/MCA in-cluster objects.
func (r *ManifestRequestTemplateReconciler) handleInitStateCreateClusterResources(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	// Acquire Lock
	if lockAcquired, err := r.acquireLock(ctx, mrt, governancev1alpha1.InitStateCreateDefaultClusterResources, "Creating MSR/MCA in cluster"); !lockAcquired || err != nil {
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	// Create linked default MSR/MCA
	if err := r.createLinkedDefaultResources(ctx, mrt); err != nil {
		err = fmt.Errorf("create linked default resources: %w", err)
		_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.InitStateCreateDefaultClusterResources, err)
		return ctrl.Result{}, err
	}

	// Release lock on success and move to the next state.
	err := r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.StateInitSetFinalizer)
	return ctrl.Result{Requeue: true}, err
}

// handleInitStateCreateClusterResources is responsible for creating the default MSR/MCA in-cluster objects.
func (r *ManifestRequestTemplateReconciler) createLinkedDefaultResources(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) error {
	application, err := r.getApplication(ctx, mrt)
	if err != nil {
		return fmt.Errorf("fetch Application associated with ManifestRequestTemplate: %w", err)
	}

	// Fetch the latest revision from the repository.
	revision, err := r.repository(ctx, mrt).GetLatestRevision(ctx)
	if err != nil {
		return fmt.Errorf("fetch latest commit from the repository: %w", err)
	}

	// Fetch all changed files in the repository, that where created before.
	fileChanges, err := r.repository(ctx, mrt).GetChangedFiles(ctx, "", revision, application.Spec.Source.Path)
	if err != nil {
		return fmt.Errorf("fetch changes between init commit and %s: %w", revision, err)
	}

	// Build default MSR out of MRT
	msr := r.buildInitialMSR(mrt, fileChanges, revision)
	// Set MRT as MSR owner
	if err := ctrl.SetControllerReference(mrt, msr, r.Scheme); err != nil {
		r.logger.Error(err, "Failed to set owner reference on MSR")
		return fmt.Errorf("while setting controllerReference for ManifestSigningRequest: %w", err)
	}
	// Save MSR in cluster
	if err := r.Create(ctx, msr); err != nil {
		if !errors.IsAlreadyExists(err) {
			r.logger.Error(err, "Failed to create initial MSR")
			return fmt.Errorf("while creating default ManifestSigningRequest: %w", err)
		}
	}

	// Build default MCA out of MRT
	mca := r.buildInitialMCA(mrt, msr, fileChanges, revision)
	// Set MRT as MCA owner
	if err := ctrl.SetControllerReference(mrt, mca, r.Scheme); err != nil {
		r.logger.Error(err, "Failed to set owner reference on MCA")
		return fmt.Errorf("while setting controllerReference for ManifestChangeApproval: %w", err)
	}
	// Save MCA in cluster
	if err := r.Create(ctx, mca); err != nil {
		if !errors.IsAlreadyExists(err) {
			r.logger.Error(err, "Failed to create initial MCA")
			return fmt.Errorf("while creating default ManifestChangeApproval: %w", err)
		}
	}

	return nil
}

// handleStateFinalizing is responsible for managing lock and setting the GovernanceFinalizer on MRT.
func (r *ManifestRequestTemplateReconciler) handleStateFinalizing(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	// Acquire Lock
	if lockAcquired, err := r.acquireLock(ctx, mrt, governancev1alpha1.StateInitSetFinalizer, "Applying finalizer"); !lockAcquired || err != nil {
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	// Add GovernanceFinalizer on MRT
	controllerutil.AddFinalizer(mrt, GovernanceFinalizer)
	if err := r.Update(ctx, mrt); err != nil {
		err = fmt.Errorf("add finalizer in initialization: %w", err)
		_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.StateInitSetFinalizer, err)
		return ctrl.Result{}, err
	}

	// Release lock on success and free the state.
	err := r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.EmptyActionState)
	return ctrl.Result{}, err
}

// acquireLock sets in cluster MRT ActionState and Condition.Progressing to True, if it isn't set yet.
func (r *ManifestRequestTemplateReconciler) acquireLock(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	newState governancev1alpha1.ActionState,
	message string,
) (bool, error) {
	// Check, if Condition.Progressing was already set.
	if meta.IsStatusConditionTrue(mrt.Status.Conditions, governancev1alpha1.Progressing) {
		return false, nil
	}

	// Set ActionState.
	mrt.Status.ActionState = newState
	meta.SetStatusCondition(&mrt.Status.Conditions, metav1.Condition{
		Type: governancev1alpha1.Progressing, Status: metav1.ConditionTrue, Reason: string(newState), Message: message,
	})

	// Save new Status changes.
	if err := r.Status().Update(ctx, mrt); err != nil {
		r.logger.Error(err, "Failed to acquire lock", "state", newState)
		return false, fmt.Errorf("update ManifestRequestTemplate after lock acquired: %w", err)
	}
	return true, nil
}

func (r *ManifestRequestTemplateReconciler) releaseLockWithFailure(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	nextState governancev1alpha1.ActionState,
	cause error,
) error {
	return r.releaseLockAbstract(ctx, mrt, nextState, "StepFailed", fmt.Sprintf("Error occurred: %v", cause))
}

func (r *ManifestRequestTemplateReconciler) releaseLockAndSetNextState(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	nextState governancev1alpha1.ActionState,
) error {
	return r.releaseLockAbstract(ctx, mrt, nextState, "StepComplete", "Step completed, proceeding to next state")
}

func (r *ManifestRequestTemplateReconciler) releaseLockAbstract(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	nextState governancev1alpha1.ActionState,
	reason, message string,
) error {
	// Re-fetch is crucial to avoid "object modified" errors
	freshMRT := &governancev1alpha1.ManifestRequestTemplate{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(mrt), freshMRT); err != nil {
		return fmt.Errorf("fetch fresh ManifestRequestTemplate: %w", err)
	}

	freshMRT.Status.ActionState = nextState
	meta.SetStatusCondition(&freshMRT.Status.Conditions, metav1.Condition{
		Type: governancev1alpha1.Progressing, Status: metav1.ConditionFalse, Reason: reason, Message: message,
	})

	return r.Status().Update(ctx, freshMRT)
}

func (r *ManifestRequestTemplateReconciler) buildInitialMSR(
	mrt *governancev1alpha1.ManifestRequestTemplate,
	fileChanges []governancev1alpha1.FileChange,
	revision string,
) *governancev1alpha1.ManifestSigningRequest {

	mrtMetaRef := governancev1alpha1.VersionedManifestRef{
		Name:      mrt.ObjectMeta.Name,
		Namespace: mrt.ObjectMeta.Namespace,
		Version:   mrt.Spec.Version,
	}

	return &governancev1alpha1.ManifestSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mrt.Spec.MSR.Name,
			Namespace: mrt.Spec.MSR.Namespace,
		},
		Spec: governancev1alpha1.ManifestSigningRequestSpec{
			Version:   0,
			CommitSHA: revision,
			MRT:       *mrtMetaRef.DeepCopy(),
			PublicKey: mrt.Spec.PGP.PublicKey,
			GitRepository: governancev1alpha1.GitRepository{
				SSHURL: mrt.Spec.GitRepository.SSHURL,
			},
			Location:  *mrt.Spec.Location.DeepCopy(),
			Changes:   fileChanges,
			Governors: *mrt.Spec.Governors.DeepCopy(),
			Require:   *mrt.Spec.Require.DeepCopy(),
			Status:    governancev1alpha1.Approved,
		},
	}
}

func (r *ManifestRequestTemplateReconciler) buildInitialMCA(
	mrt *governancev1alpha1.ManifestRequestTemplate,
	msr *governancev1alpha1.ManifestSigningRequest,
	fileChanges []governancev1alpha1.FileChange,
	revision string,
) *governancev1alpha1.ManifestChangeApproval {

	mrtMetaRef := governancev1alpha1.VersionedManifestRef{
		Name:      mrt.ObjectMeta.Name,
		Namespace: mrt.ObjectMeta.Namespace,
		Version:   mrt.Spec.Version,
	}

	msrMetaRef := governancev1alpha1.VersionedManifestRef{
		Name:      msr.ObjectMeta.Name,
		Namespace: msr.ObjectMeta.Namespace,
		Version:   msr.Spec.Version,
	}

	return &governancev1alpha1.ManifestChangeApproval{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mrt.Spec.MCA.Name,
			Namespace: mrt.Spec.MCA.Namespace,
		},
		Spec: governancev1alpha1.ManifestChangeApprovalSpec{
			Version:   0,
			MRT:       *mrtMetaRef.DeepCopy(),
			MSR:       *msrMetaRef.DeepCopy(),
			PublicKey: mrt.Spec.PGP.PublicKey,
			GitRepository: governancev1alpha1.GitRepository{
				SSHURL: mrt.Spec.GitRepository.SSHURL,
			},
			LastApprovedCommitSHA: revision, // revision, on which MRT should have been created
			Location:              *mrt.Spec.Location.DeepCopy(),
			Changes:               fileChanges,
			Governors:             *mrt.Spec.Governors.DeepCopy(),
			Require:               *mrt.Spec.Require.DeepCopy(),
		},
	}
}

func (r *ManifestRequestTemplateReconciler) reconcileNormal(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	req ctrl.Request,
) (ctrl.Result, error) {
	// Check an active Lock.
	if meta.IsStatusConditionTrue(mrt.Status.Conditions, governancev1alpha1.Progressing) {
		r.logger.Info("Waiting for an ongoing operation to complete.", "operation", mrt.Status.ActionState)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// If ActionState is empty, we need to decide what action to do.
	if mrt.Status.ActionState == governancev1alpha1.EmptyActionState {
		// If the revision queue is not empty - pick the revision and process it. // TODO: move to RabbitMQ queue.
		if len(mrt.Status.RevisionsQueue) > 0 {
			r.logger.Info("New revisions found in queue. Transitioning to ProcessingRevision state.")
			return ctrl.Result{}, r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.StateProcessingRevision)
		}

		// No new revision found - periodically check dependencies.
		// TODO
		// r.logger.Info("No pending revisions. Transitioning to CheckingDependencies state.")
		// return ctrl.Result{}, r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.StateCheckingDependencies)
	}

	// Execute action.
	switch mrt.Status.ActionState {
	case governancev1alpha1.StateProcessingRevision:
		return r.handleStateProcessingRevision(ctx, mrt)
	// TODO
	// case governancev1alpha1.StateCheckingDependencies:
	// 	return r.handleStateCheckingDependencies(ctx, mrt)
	default:
		// If the state is unknown - set to EmptyState.
		return ctrl.Result{}, r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.EmptyActionState)
	}
}

// handleStateProcessingRevision is responsible for managing lock and processing revision request.
func (r *ManifestRequestTemplateReconciler) handleStateProcessingRevision(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	// Acquire Lock.
	if lockAcquired, err := r.acquireLock(ctx, mrt, governancev1alpha1.StateProcessingRevision, "Processing new Git revision"); !lockAcquired || err != nil {
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	// Check all dependencies exist.
	if err := r.checkDependencies(ctx, mrt); err != nil {
		r.logger.Error(err, "Failed on dependency check. Requeuing.")
		err = fmt.Errorf("check dependency: %w", err)
		_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.StateProcessingRevision, err)
		return ctrl.Result{}, err
	}

	// Check repository connection exists.
	if _, err := r.repositoryWithError(ctx, mrt); err != nil {
		r.logger.Error(err, "Failed to connect to repository.")
		err = fmt.Errorf("check repository: %w", err)
		_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.StateProcessingRevision, err)
		return ctrl.Result{}, err
	}

	// Get the revision we are working on.
	revision := mrt.Status.RevisionsQueue[0] // TODO: should be a single value and taken from RabbitMQ queue.

	// Dispatch to the correct revision sub-state handler.
	var err error
	var result ctrl.Result

	switch mrt.Status.RevisionProcessingState {
	case governancev1alpha1.StateRevisionPreflightCheck, governancev1alpha1.StateRevisionEmpty:
		// 1. Devide, whether the MSR process should start or revision is worth to skip.
		result, err = r.handleSubStatePreflightCheck(ctx, mrt, revision)
	case governancev1alpha1.StateRevisionUpdateMSRSpec:
		// 2. Update the MSR Spec with data from new revision (changed files, revision hash, etc.).
		result, err = r.handleSubStateUpdateMSR(ctx, mrt, revision)
	case governancev1alpha1.StateRevisionAfterMSRUpdate:
		// 3. After MSR is updated, refresh MSR related information in MRT.
		result, err = r.handleSubStateFinalizeMRT(ctx, mrt, revision)
	default:
		// Any unknown RevisionProcessingState during state processing - error.
		r.logger.Info(fmt.Sprintf("Unknown RevisionProcessingState state: %s", string(mrt.Status.RevisionProcessingState)))
		return ctrl.Result{}, fmt.Errorf("unknown RevisionProcessingState state: %s", string(mrt.Status.RevisionProcessingState))

	}

	// If any step failed, we release the lock but keep the meta-state.
	if err != nil {
		_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.StateProcessingRevision, err)
		return result, err
	}

	return result, nil
}

// handleInitStateGitCommit is responsible for releasing lock (set in previous function) and deciding,
// whether MSR process should be started. If yes - sets RevisionProcessingState to be StateRevisionUpdateMSRSpec.
func (r *ManifestRequestTemplateReconciler) handleSubStatePreflightCheck(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	revision string,
) (ctrl.Result, error) {
	// Evaluate the revision and return, if it should be skipped.
	shouldSkip, reason, err := r.shouldSkipRevision(ctx, mrt, revision)
	if err != nil {
		r.logger.Error(err, "Failure while evaluating new revision from revision queue")
		err = fmt.Errorf("evaluate new revision from revision queue: %w", err)
		_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.StateProcessingRevision, err)
		return ctrl.Result{}, err
	}

	if shouldSkip {
		r.logger.Info("Skipping revision based on pre-flight check", "revision", revision, "reason", reason)
		// Pop the revision from the queue and reset the state to EmptyPending to check the next one.
		mrt.Status.RevisionsQueue = mrt.Status.RevisionsQueue[1:] // TODO: just ack the queue
		err := r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.EmptyActionState)
		return ctrl.Result{}, err
	}

	// Revision passes. Transition RevisionProcessingState to StateRevisionUpdateMSRSpec.
	mrt.Status.RevisionProcessingState = governancev1alpha1.StateRevisionUpdateMSRSpec
	err = r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.StateProcessingRevision)
	return ctrl.Result{Requeue: true}, nil
}

// shouldSkipRevision decides based on MRT, whether revision should be processed or skipped.
func (r *ManifestRequestTemplateReconciler) shouldSkipRevision(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	revision string,
) (bool, string, error) {
	mca, err := r.getMCA(ctx, mrt)
	if err != nil {
		return false, "", fmt.Errorf("get MCA for MRT: %w", err)
	}

	// Try to find MCA with such revision.
	mcaRevisionIdx := slices.IndexFunc(mca.Status.ApprovalHistory, func(rec governancev1alpha1.ManifestChangeApprovalHistoryRecord) bool {
		return rec.CommitSHA == revision
	})
	if mcaRevisionIdx != -1 && mcaRevisionIdx == len(mca.Status.ApprovalHistory)-1 { // Revision is last approved MCA.
		r.logger.Info("Revision corresponds to the latest MCA. Do nothing", "revision", revision)
		return true, "Revision corresponds to the latest MCA", nil
	} else if mcaRevisionIdx != -1 { // Revision comes for some non-latest MCA.
		r.logger.Info("Revision corresponds to a non latest MCA from History. Might be rollback. No support yet. Do nothing", "revision", revision) // TODO: rollback case
		return true, "Revision corresponds to a non latest MCA from History. Might be rollback. No support yet", nil
	}

	// Verify, if revision was already processed.
	if revision == mrt.Status.LastObservedCommitHash {
		r.logger.Info("Revision corresponds to the latest processed revision. Do nothing", "revision", revision)
		return true, "Revision corresponds to the latest processed revision", nil
	}

	// Check if revision exists in the repository.
	if hasRevision, err := r.repository(ctx, mrt).HasRevision(ctx, revision); err != nil {
		r.logger.Error(err, "Failed to check if repository has revision", "revision", revision)
		return false, "", fmt.Errorf("get latest revision from repository: %w", err)
	} else if !hasRevision {
		// Revision is not part of the main branch. Skip it.
		return true, fmt.Sprintf("No commit for revision %s in the repository", revision), nil
	}

	// TODO: should be considered better the case, when the revision is not the latest revision.
	// It can cause many of requests comming to controller, when controller creates its own resources.
	// Usefull only, when someone tries to do MSR process for some old commit.
	// TODO: but when it might be helpfull?
	// {
	// 	// Take the latest revision from the repository.
	// 	latestRevision, err := r.repository(ctx, mrt).GetLatestRevision(ctx)
	// 	if err != nil {
	// 		r.logger.Error(err, "Failed to fetch last revision from repository", "revision", revision)
	// 		return false, "", fmt.Errorf("Fetch latest revision from repository: %w", err)
	// 	}

	// 	// Check, if revision comes before latestRevision.
	// 	if latestRevision != revision {
	// 		// If latestRevision was not processed yet - try to add for MSR process.
	// 		if latestRevision != mrt.Status.LastObservedCommitHash {
	// 			r.logger.Info("Detected newer latest revision in repository", "revision", revision, "latestRevision", latestRevision)

	// 			// If latest revision already has MCA - do nothing.
	// 			// TODO: should add later in RabbitMQ. Should be managed, how we check revisions in the
	// 			latestRevisionIdx := slices.IndexFunc(mca.Status.ApprovalHistory, func(rec governancev1alpha1.ManifestChangeApprovalHistoryRecord) bool {
	// 				return rec.CommitSHA == latestRevision
	// 			})
	// 			if latestRevisionIdx != -1 {
	// 				return true, fmt.Sprintf("Latest revision not tracked, but has ManifestChangeApproval idx: %d", latestRevisionIdx), nil
	// 			}

	// 			// Remove old revision and add latestRevision to the RevisionQueue.
	// 			mrt.Status.RevisionsQueue = append(mrt.Status.RevisionsQueue, latestRevision)
	// 			return true, fmt.Sprintf("Newer revision exists: %w"), nil
	// 		}

	// 		// laterRevision is already processed - remove old revision and do nothing.
	// 		r.logger.Info("Revision corresponds to some old revision from repository. Do nothing", "revision", revision)
	// 		return true, fmt.Sprintf("Revision corresponds to some old revision from repository: %w"), nil
	// 	}
	// }

	return false, "", nil
}

// handleSubStateUpdateMSR is responsible for releasing lock (set in previous function) and updating MSR with new data.
// If there are no changed files in revision - skip this revision and transition to an empty state.
// If there are changed files - transition to StateRevisionAfterMSRUpdate.
func (r *ManifestRequestTemplateReconciler) handleSubStateUpdateMSR(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	revision string,
) (ctrl.Result, error) {
	continueMSR, err := r.performMSRUpdate(ctx, mrt, revision)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("perform ManifestSigningRequest update: %w", err) // Let the main handler release the lock
	} else if !continueMSR {
		r.logger.Info("Skipping revision since there is no govern files in revision", "revision", revision)
		// Pop the revision from the queue and reset the state to EmptyPending to check the next one.
		mrt.Status.RevisionsQueue = mrt.Status.RevisionsQueue[1:] // TODO: just ack the queue
		err := r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.EmptyActionState)
		return ctrl.Result{}, err
	}

	// Revision passes. Transition RevisionProcessingState to StateRevisionAfterMSRUpdate.
	mrt.Status.RevisionProcessingState = governancev1alpha1.StateRevisionAfterMSRUpdate
	err = r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.StateProcessingRevision)
	return ctrl.Result{Requeue: true}, nil
}

// performMSRUpdate takes all the changed files for revision.
// If there any govern manifests (manifests files, under ArgoCD controll and not in the governanceFolder / qubmango folder),
// it updates MSR attached to current MRT with new Spec.
// First return parameter reflects, whether MSR should be processed further (true),
// or skipped (false), in case if there is no files to govern. In case of error this parameter is false.
func (r *ManifestRequestTemplateReconciler) performMSRUpdate(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	revision string,
) (bool, error) {
	r.logger.Info("Start MSR process", "revision", revision)

	application, err := r.getApplication(ctx, mrt)
	if err != nil {
		return false, fmt.Errorf("fetch Application associated with ManifestRequestTemplate: %w", err)
	}

	mca, err := r.getMCA(ctx, mrt)
	if err != nil {
		return false, fmt.Errorf("get ManifestChangeApproval for ManifestRequestTemplate: %w", err)
	}

	// Get changed аiles from git repository.
	changedFiles, err := r.repository(ctx, mrt).GetChangedFiles(ctx, mca.Spec.LastApprovedCommitSHA, revision, application.Spec.Source.Path)
	if err != nil {
		r.logger.Error(err, "Failed to get changed files from repository")
		return false, fmt.Errorf("get changed files: %w", err)
	}

	// Filter all files, that ArgoCD shouldn't accept/monitor + content of governanceFolder.
	changedFiles = r.filterNonManifestFiles(changedFiles, mrt)
	// If there is no files for governance - skip this revision.
	if len(changedFiles) == 0 {
		mrt.Status.LastObservedCommitHash = revision
		r.logger.Info("No manifest file changes detected between commits. Skipping MSR creation.")
		return false, nil
	}

	// Get and update MSR in cluster
	msr, err := r.getMSR(ctx, mrt)
	if err != nil {
		r.logger.Error(err, "Failed to fetch ManifestSigningRequest by ManifestRequestTemplate")
		return false, fmt.Errorf("get ManifestSigningRequest for ManifestRequestTemplate: %w", err)
	}

	// Created updated version of MSR with higher version
	updatedMSR := r.getMSRWithNewVersion(mrt, msr, revision, changedFiles)

	// Update MSR spec in cluster. Trigger so push of new MSR to git repository from MSR controller
	if err := r.Update(ctx, updatedMSR); err != nil {
		r.logger.Error(err, "Failed to update MSR spec in cluster after successful revision processed")
		return false, fmt.Errorf("after successful MSR revision processed: %w", err)
	}

	return true, nil
}

// handleSubStateFinalizeMRT is responsible for releasing lock (set in previous function) and
// updating MRT's LastObservedCommitHash, RevisionsQueue and setting ActionState, RevisionProcessingState to empty states.
func (r *ManifestRequestTemplateReconciler) handleSubStateFinalizeMRT(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, revision string) (ctrl.Result, error) {
	// Update MRT LastObservedCommitHash and pop the revision from the queue.
	mrt.Status.LastObservedCommitHash = revision
	mrt.Status.RevisionsQueue = mrt.Status.RevisionsQueue[1:]
	// Set RevisionProcessingState to an empty state.
	mrt.Status.RevisionProcessingState = governancev1alpha1.StateRevisionEmpty

	// Release lock on success and free the state.
	err := r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.EmptyActionState)
	return ctrl.Result{Requeue: true}, err
}

// checkDependencies validates that all linked resources for an MRT exist.
func (r *ManifestRequestTemplateReconciler) checkDependencies(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) error {
	// Check for Application
	app := &argocdv1alpha1.Application{}
	err := r.Get(ctx, types.NamespacedName{Name: mrt.Spec.ArgoCDApplication.Name, Namespace: mrt.Spec.ArgoCDApplication.Namespace}, app)
	if err != nil {
		r.logger.Error(err, "Failed to find linked Application")
		return fmt.Errorf("couldn't find linked Application")
	}

	// Check for MSR
	msr := &governancev1alpha1.ManifestSigningRequest{}
	err = r.Get(ctx, types.NamespacedName{Name: mrt.Spec.MSR.Name, Namespace: mrt.Spec.MSR.Namespace}, msr)
	if err != nil {
		r.logger.Error(err, "Failed to find linked MSR")
		return fmt.Errorf("couldn't find linked ManifestSigningRequest: %w", err)
	}

	// Check for MCA
	mca := &governancev1alpha1.ManifestChangeApproval{}
	err = r.Get(ctx, types.NamespacedName{Name: mrt.Spec.MCA.Name, Namespace: mrt.Spec.MCA.Namespace}, mca)
	if err != nil {
		r.logger.Error(err, "Failed to find linked MCA")
		return fmt.Errorf("couldn't find linked ManifestChangeApproval: %w", err)
	}

	return nil
}

func (r *ManifestRequestTemplateReconciler) getMSRWithNewVersion(
	mrt *governancev1alpha1.ManifestRequestTemplate,
	msr *governancev1alpha1.ManifestSigningRequest,
	revision string,
	changedFiles []governancev1alpha1.FileChange,
) *governancev1alpha1.ManifestSigningRequest {
	mrtSpecCpy := mrt.Spec.DeepCopy()
	updatedMSR := msr.DeepCopy()

	updatedMSR.Spec.Version = updatedMSR.Spec.Version + 1
	updatedMSR.Spec.CommitSHA = revision
	updatedMSR.Spec.MRT = governancev1alpha1.VersionedManifestRef{
		Name:      mrt.ObjectMeta.Name,
		Namespace: mrt.ObjectMeta.Namespace,
		Version:   mrt.Spec.Version,
	}
	updatedMSR.Spec.PublicKey = mrtSpecCpy.PGP.PublicKey
	updatedMSR.Spec.GitRepository = governancev1alpha1.GitRepository{
		SSHURL: mrt.Spec.GitRepository.SSHURL,
	}
	updatedMSR.Spec.Location = *mrtSpecCpy.Location.DeepCopy()
	updatedMSR.Spec.Changes = changedFiles
	updatedMSR.Spec.Governors = *mrtSpecCpy.Governors.DeepCopy()
	updatedMSR.Spec.Require = *mrtSpecCpy.Require.DeepCopy()
	updatedMSR.Spec.Status = governancev1alpha1.InProgress

	return updatedMSR
}

func (r *ManifestRequestTemplateReconciler) filterNonManifestFiles(
	files []governancev1alpha1.FileChange,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) []governancev1alpha1.FileChange {

	var filtered []governancev1alpha1.FileChange

	// normalize paths
	governanceFolder := filepath.Clean(mrt.Spec.Location.Folder)

	for _, file := range files {
		filePath := filepath.Clean(file.Path)

		// ArgoCD can process .yaml, .yml, and .json files
		isManifestType := strings.HasSuffix(filePath, ".yaml") ||
			strings.HasSuffix(filePath, ".yml") ||
			file.Kind != ""

		if !isManifestType {
			continue
		}

		// Skip files inside of governanceFolder or operational folder
		// TODO: we suppose, that MRT is created outside of governanceFolder. Otherwise, it will be skipped.
		// TODO: on creation check, that MSR is not created inside of governanceFolder. Or improve the logic
		isGovernanceFile := strings.HasPrefix(filePath, governanceFolder+"/")
		isQubmangoOperationalFile := strings.HasPrefix(filePath, QubmangoOperationalFolder+"/")

		if isGovernanceFile || isQubmangoOperationalFile {
			continue
		}

		// Add the remaining file (after both checks) to the list
		filtered = append(filtered, file)
	}

	return filtered
}

// getApplication fetches the ArgoCD Application resource referenced by the ManifestRequestTemplate
func (r *ManifestRequestTemplateReconciler) getApplication(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (*argocdv1alpha1.Application, error) {
	app := &argocdv1alpha1.Application{}
	appKey := types.NamespacedName{
		Name:      mrt.Spec.ArgoCDApplication.Name,
		Namespace: mrt.Spec.ArgoCDApplication.Namespace,
	}

	if err := r.Get(ctx, appKey, app); err != nil {
		r.logger.Error(err, "Failed to get ArgoCD Application", "applicationNamespacedName", appKey)
		return nil, fmt.Errorf("fetch Application associated with ManifestRequestTemplate: %w", err)
	}

	return app, nil
}

func (r *ManifestRequestTemplateReconciler) getMSR(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (*governancev1alpha1.ManifestSigningRequest, error) {
	msr := &governancev1alpha1.ManifestSigningRequest{}
	msrKey := types.NamespacedName{
		Name:      mrt.Spec.MSR.Name,
		Namespace: mrt.Spec.MSR.Namespace,
	}

	if err := r.Get(ctx, msrKey, msr); err != nil {
		r.logger.Error(err, "Failed to get ManifestSigningRequest", "manifestSigningRequestNamespacedName", msrKey)
		return nil, fmt.Errorf("fetch ManifestSigningRequest associated with ManifestRequestTemplate: %w", err)
	}

	return msr, nil
}

func (r *ManifestRequestTemplateReconciler) getMCA(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (*governancev1alpha1.ManifestChangeApproval, error) {
	mca := &governancev1alpha1.ManifestChangeApproval{}
	mcaKey := types.NamespacedName{
		Name:      mrt.Spec.MCA.Name,
		Namespace: mrt.Spec.MCA.Namespace,
	}

	if err := r.Get(ctx, mcaKey, mca); err != nil {
		r.logger.Error(err, "Failed to get ManifestChangeApproval", "manifestChangeApprovalNamespacedName", mcaKey)
		return nil, fmt.Errorf("fetch ManifestChangeApproval associated with ManifestRequestTemplate: %w", err)
	}

	return mca, nil
}

func (r *ManifestRequestTemplateReconciler) repositoryWithError(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (repomanager.GitRepository, error) {
	return r.RepoManager.GetProviderForMRT(ctx, mrt)
}

func (r *ManifestRequestTemplateReconciler) repository(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) repomanager.GitRepository {
	repo, _ := r.RepoManager.GetProviderForMRT(ctx, mrt)
	return repo
}
