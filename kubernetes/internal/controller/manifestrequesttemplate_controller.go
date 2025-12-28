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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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
		Watches(
			&governancev1alpha1.GovernanceQueue{},
			handler.EnqueueRequestsFromMapFunc(r.findMRTForQueue),
		).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Named("manifestrequesttemplate").
		Complete(r)
}

// findQueueForEvent is a mapping function to assign an Event to a Queue.
func (r *ManifestRequestTemplateReconciler) findMRTForQueue(ctx context.Context, obj client.Object) []reconcile.Request {
	queue, ok := obj.(*governancev1alpha1.GovernanceQueue)
	if !ok {
		// Not a queue.
		return nil
	}

	// Get EventQueue from Client.
	mrtKey := types.NamespacedName{Name: queue.Spec.MRT.Name, Namespace: queue.Spec.MRT.Namespace}
	r.logger.WithValues("mrt", mrtKey, "queue", types.NamespacedName{Namespace: queue.Namespace, Name: queue.Name})
	var mrt governancev1alpha1.ManifestRequestTemplate
	if err := r.Get(ctx, mrtKey, &mrt); err != nil {
		r.logger.Error(err, "Failed to fetch MRT for GovernanceQueue")
		return nil
	}

	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name:      mrt.Name,
				Namespace: mrt.Namespace,
			},
		},
	}
}

// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestrequesttemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestrequesttemplates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestrequesttemplates/finalizers,verbs=update
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestsigningrequests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestchangeapprovals,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=governancequeues,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=governancequeues/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=governanceevents,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=governanceevents/status,verbs=get;update;patch

func (r *ManifestRequestTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = log.FromContext(ctx).WithValues("controller", "ManifestRequestTemplate", "name", req.Name, "namespace", req.Namespace)

	r.logger.Info("Reconciling ManifestRequestTemplate")

	// Fetch the MRT instance
	mrt := &governancev1alpha1.ManifestRequestTemplate{}
	if err := r.Get(ctx, req.NamespacedName, mrt); err != nil {
		r.logger.Info("DEBUG: MRT not found")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if !mrt.ObjectMeta.DeletionTimestamp.IsZero() {
		r.logger.Info("DEBUG: MRT reconcileDelete")
		return r.reconcileDelete(ctx, mrt)
	}

	// Release lock if hold too long
	if r.isLockForMoreThan(mrt, 30*time.Second) {
		r.logger.Info("DEBUG: MRT removeProgressing")
		return ctrl.Result{Requeue: true}, r.releaseLockWithFailure(ctx, mrt, mrt.Status.ActionState, fmt.Errorf("lock acquired for too long"))
	}

	// Handle initialization
	if !controllerutil.ContainsFinalizer(mrt, GovernanceFinalizer) {
		r.logger.Info("DEBUG: MRT reconcileCreate")
		return r.reconcileCreate(ctx, mrt)
	}

	// Handle normal reconciliation
	r.logger.Info("DEBUG: MRT reconcileNormal")
	return r.reconcileNormal(ctx, mrt)
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

// handleStateDeletion removes entry for current MRT from index file and finalizer from this resource.
func (r *ManifestRequestTemplateReconciler) handleStateDeletion(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	// Skip lock acquiring.

	// Clean up the entry in the QubmangoOperationalFile file.
	r.logger.Info("Start removing entry from Git index file.")
	governanceIndexAlias := mrt.Namespace + ":" + mrt.Name
	if _, _, err := r.repository(ctx, mrt).RemoveFromIndexFile(ctx, QubmangoOperationalFile, governanceIndexAlias); err != nil {
		_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.StateDeletionInProgress, err)
		return ctrl.Result{}, fmt.Errorf("failed to remove entry from index file: %w", err)
	}
	r.logger.Info("Finish removing entry from Git index file.")

	// Remove the finalizer from MRT.
	r.logger.Info("Start removing finalizer.")
	controllerutil.RemoveFinalizer(mrt, GovernanceFinalizer)
	if err := r.Update(ctx, mrt); err != nil {
		_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.StateDeletionInProgress, err)
		return ctrl.Result{}, fmt.Errorf("remove finalizer from ManifestRequestTemplate: %w", err)
	}
	r.logger.Info("Finish removing finalizer.")

	r.logger.Info("Successfully deleted ManifestRequestTemplate.")

	err := r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.EmptyActionState)
	return ctrl.Result{}, err
}

func (r *ManifestRequestTemplateReconciler) isLockForMoreThan(
	mrt *governancev1alpha1.ManifestRequestTemplate,
	duration time.Duration,
) bool {
	condition := meta.FindStatusCondition(mrt.Status.Conditions, governancev1alpha1.Progressing)
	return condition != nil && time.Now().Sub(condition.LastTransitionTime.Time) >= duration
}

// reconcileCreate handles the logic for a newly created MRT, that has not been initialized.
func (r *ManifestRequestTemplateReconciler) reconcileCreate(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	r.logger.Info("Reconciling new ManifestRequestTemplate", "currentState", mrt.Status.ActionState)

	// Check, if there is any available git repository provider
	if _, err := r.repositoryWithError(ctx, mrt); err != nil {
		r.logger.Error(err, "Failed on first repository fetch")
		return ctrl.Result{}, fmt.Errorf("init repo for ManifestRequestTemplate: %w", err)
	}

	switch mrt.Status.ActionState {
	case governancev1alpha1.EmptyActionState, governancev1alpha1.StateInitGitGovernanceInitialization:
		// 1. Create an entry in QubmangoOperationalFile file and save the commit as LastObservedCommitHash.
		r.logger.Info("handleInitStateGitCommit")
		return r.handleInitStateGitCommit(ctx, mrt)
	case governancev1alpha1.InitStateCreateDefaultClusterResources:
		// 2. Create default MSR, MCA resources in the cluster.
		r.logger.Info("handleInitStateCreateClusterResources")
		return r.handleInitStateCreateClusterResources(ctx, mrt)
	case governancev1alpha1.StateInitSetFinalizer:
		// 3. Confirm MRT initialization, by setting the GovernanceFinalizer.
		r.logger.Info("handleStateFinalizing")
		return r.handleStateFinalizing(ctx, mrt)
	default:
		// Any unknown ActionState in MRT during initialization - error.
		r.logger.Info(fmt.Sprintf("Unknown initialization state: %s", string(mrt.Status.ActionState)))
		return ctrl.Result{}, fmt.Errorf("unknown initialization state: %s", string(mrt.Status.ActionState))
	}
}

// handleInitStateGitCommit is responsible for managing lock and creating an entry in the QubmangoOperationalFile file.
func (r *ManifestRequestTemplateReconciler) handleInitStateGitCommit(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	// Acquire Lock
	if lockAcquired, err := r.acquireLock(ctx, mrt, governancev1alpha1.StateInitGitGovernanceInitialization, "Creating entry in Qubmango operational file in Git"); !lockAcquired || err != nil {
		r.logger.Info("lock was already acquired")
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
	if err := r.Status().Update(ctx, mrt); err != nil {
		err = fmt.Errorf("update MRT LastObservedCommitHash: %w", err)
		_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.StateInitGitGovernanceInitialization, err)
		return ctrl.Result{}, err
	}

	err = r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.InitStateCreateDefaultClusterResources)
	if err != nil {
		r.logger.Info("DEBUG: Unsuccessfull handleInitStateGitCommit")
	} else {
		r.logger.Info("DEBUG: Successfull handleInitStateGitCommit")
	}
	return ctrl.Result{Requeue: true}, err
}

// handleInitStateCreateClusterResources is responsible for managing lock and creating the default MSR/MCA in-cluster objects.
func (r *ManifestRequestTemplateReconciler) handleInitStateCreateClusterResources(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	// Acquire Lock
	if lockAcquired, err := r.acquireLock(ctx, mrt, governancev1alpha1.InitStateCreateDefaultClusterResources, "Creating MSR/MCA/GovernanceQueue in cluster"); !lockAcquired || err != nil {
		r.logger.Info("lock was already acquired")
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
	if err != nil {
		r.logger.Info("DEBUG: Unsuccessfull handleInitStateCreateClusterResources")
	} else {
		r.logger.Info("DEBUG: Successfull handleInitStateCreateClusterResources")
	}
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

	// Build initial GovernanceQueue
	queue := r.buildInitialGovernanceQueue(mrt)
	// Set MRT as GovernanceQueue owner
	if err := ctrl.SetControllerReference(mrt, mca, r.Scheme); err != nil {
		r.logger.Error(err, "Failed to set owner reference on GovernanceQueue")
		return fmt.Errorf("while setting controllerReference for GovernanceQueue: %w", err)
	}
	// Save GovernanceQueue in cluster
	if err := r.Create(ctx, queue); err != nil {
		if !errors.IsAlreadyExists(err) {
			r.logger.Error(err, "Failed to create initial GovernanceQueue")
			return fmt.Errorf("while creating default GovernanceQueue: %w", err)
		}
	}

	// Update MRT GovernanceQueue ref
	mrt.Status.RevisionQueueRef = governancev1alpha1.ManifestRefOptional{
		Name:      queue.Name,
		Namespace: queue.Namespace,
	}
	if err := r.Status().Update(ctx, mrt); err != nil {
		return fmt.Errorf("update MRT RevisionQueueRef: %w", err)
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
		r.logger.Info("lock was already acquired")
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
	if err != nil {
		r.logger.Info("DEBUG: Unsuccessfull handleStateFinalizing")
	} else {
		r.logger.Info("DEBUG: Successfull handleStateFinalizing")
	}

	return ctrl.Result{}, err
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

func (r *ManifestRequestTemplateReconciler) buildInitialGovernanceQueue(
	mrt *governancev1alpha1.ManifestRequestTemplate,
) *governancev1alpha1.GovernanceQueue {

	mrtMetaRef := governancev1alpha1.ManifestRef{
		Name:      mrt.ObjectMeta.Name,
		Namespace: mrt.ObjectMeta.Namespace,
	}

	return &governancev1alpha1.GovernanceQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "queue-" + mrt.Name,
			Namespace: mrt.Namespace,
		},
		Spec: governancev1alpha1.GovernanceQueueSpec{
			MRT: mrtMetaRef,
		},
	}
}

func (r *ManifestRequestTemplateReconciler) reconcileNormal(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	// Check an active Lock.
	if meta.IsStatusConditionTrue(mrt.Status.Conditions, governancev1alpha1.Progressing) {
		r.logger.Info("Waiting for an ongoing operation to complete.", "operation", mrt.Status.ActionState)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// If ActionState is empty, we need to decide what action to do.
	if mrt.Status.ActionState == governancev1alpha1.EmptyActionState {
		// If the revision queue is not empty - start revision process.
		newRevision, err := r.anyRevisionEvents(ctx, mrt)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("is any revision event present: %w", err)
		}

		if newRevision {
			r.logger.Info("New revisions found in queue. Transitioning to ProcessingRevision state.")
			return ctrl.Result{Requeue: true}, r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.StateProcessingRevision)
		}
	}

	// Execute action.
	switch mrt.Status.ActionState {
	case governancev1alpha1.StateProcessingRevision:
		return r.handleStateProcessingRevision(ctx, mrt)
	default:
		// If the state is unknown - set to EmptyState.
		return ctrl.Result{}, r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.EmptyActionState)
	}
}

func (r *ManifestRequestTemplateReconciler) anyRevisionEvents(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (bool, error) {
	var queue governancev1alpha1.GovernanceQueue
	queueRef := mrt.Status.RevisionQueueRef
	if err := r.Get(ctx, types.NamespacedName{Namespace: queueRef.Namespace, Name: queueRef.Name}, &queue); err != nil {
		r.logger.Error(err, "Failed to get queue for MRT")
		return false, fmt.Errorf("get GovernanceQueue for ManifestRequestTemplate: %w", err)
	}

	return len(queue.Status.Queue) > 0, nil
}

func (r *ManifestRequestTemplateReconciler) revisionsQueueHead(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (*governancev1alpha1.GovernanceEvent, error) {
	// Get the queue.
	var queue governancev1alpha1.GovernanceQueue
	queueRef := mrt.Status.RevisionQueueRef
	if err := r.Get(ctx, types.NamespacedName{Namespace: queueRef.Namespace, Name: queueRef.Name}, &queue); err != nil {
		r.logger.Error(err, "Failed to get GovernanceQueue for MRT")
		return nil, fmt.Errorf("get GovernanceQueue for ManifestRequestTemplate: %w", err)
	}

	// Check if the queue is empty.
	if len(queue.Status.Queue) < 1 {
		return nil, fmt.Errorf("queue is empty: %v", queueRef)
	}

	// Get the reference to the head of the queue.
	eventRef := queue.Status.Queue[0]
	// Fetch the GovernanceEvent object.
	eventKey := types.NamespacedName{
		Name:      eventRef.Name,
		Namespace: "", // GovernanceEvents are cluster-scoped
	}
	var event governancev1alpha1.GovernanceEvent
	if err := r.Get(ctx, eventKey, &event); err != nil {
		r.logger.Error(err, "Failed to get GovernanceEvent for GovernanceQueue")
		return nil, fmt.Errorf("get GovernanceEvent for GovernanceQueue: %w", err)
	}

	if event.Spec.Type != governancev1alpha1.EventTypeNewRevision {
		return nil, fmt.Errorf("unsupported event type: %s", event.Spec.Type)
	}

	return &event, nil
}

func (r *ManifestRequestTemplateReconciler) removeRevisionsQueueHead(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) error {
	// Get the revision we are working on.
	event, err := r.revisionsQueueHead(ctx, mrt)
	if err != nil {
		return fmt.Errorf("get head of the revision queue: %w", err)
	}

	// Delete the resource and trigger reconciliation for GovernanceQueue.
	if err = r.Delete(ctx, event); err != nil {
		return fmt.Errorf("delete GovernanceEvent: %w", err)
	}

	return nil
}

// handleStateProcessingRevision is responsible for managing lock and processing revision request.
func (r *ManifestRequestTemplateReconciler) handleStateProcessingRevision(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	// Acquire Lock.
	if lockAcquired, err := r.acquireLock(ctx, mrt, governancev1alpha1.StateProcessingRevision, "Processing new Git revision"); !lockAcquired || err != nil {
		r.logger.Info("lock was already acquired")
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
	event, err := r.revisionsQueueHead(ctx, mrt)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("get head of the revision queue: %w", err)
	}

	revision := event.Spec.NewRevision.CommitSHA

	// Dispatch to the correct revision sub-state handler.
	var result ctrl.Result

	// In case of failure, let the main handler release the lock.
	switch mrt.Status.RevisionProcessingState {
	case governancev1alpha1.StateRevisionPreflightCheck, governancev1alpha1.StateRevisionEmpty:
		// 1. Devide, whether the MSR process should start or revision is worth to skip.
		r.logger.Info("DEBUG: handleStateProcessingRevision#handleSubStatePreflightCheck")
		result, err = r.handleSubStatePreflightCheck(ctx, mrt, revision)
	case governancev1alpha1.StateRevisionUpdateMSRSpec:
		// 2. Update the MSR Spec with data from new revision (changed files, revision hash, etc.).
		r.logger.Info("DEBUG: handleStateProcessingRevision#handleSubStateUpdateMSR")
		result, err = r.handleSubStateUpdateMSR(ctx, mrt, revision)
	case governancev1alpha1.StateRevisionAfterMSRUpdate:
		// 3. After MSR is updated, refresh MSR related information in MRT.
		r.logger.Info("DEBUG: handleStateProcessingRevision#handleSubstateFinish")
		result, err = r.handleSubstateFinish(ctx, mrt, revision)
	default:
		// Any unknown RevisionProcessingState during state processing - error.
		r.logger.Info(fmt.Sprintf("Unknown RevisionProcessingState state: %s", string(mrt.Status.RevisionProcessingState)))
		return ctrl.Result{}, fmt.Errorf("unknown RevisionProcessingState state: %s", string(mrt.Status.RevisionProcessingState))

	}

	// If any step failed, we release the lock but keep the meta-state.
	if err != nil {
		r.logger.Info("DEBUG: handleStateProcessingRevision: error after sub state call")
		_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.StateProcessingRevision, err)
		return result, err
	}

	r.logger.Info("DEBUG: handleStateProcessingRevision: successfull sub state call")
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
		return ctrl.Result{}, err
	}

	if shouldSkip {
		r.logger.Info("Skipping revision based on pre-flight check", "revision", revision, "reason", reason)
		// Pop the revision from the queue and reset the state to EmptyPending to check the next one.
		if err := r.removeRevisionsQueueHead(ctx, mrt); err != nil {
			return ctrl.Result{}, err
		}

		err := r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.EmptyActionState)
		return ctrl.Result{}, err
	}

	// Revision passes. Transition RevisionProcessingState to StateRevisionUpdateMSRSpec.
	mrt.Status.RevisionProcessingState = governancev1alpha1.StateRevisionUpdateMSRSpec
	if err := r.Status().Update(ctx, mrt); err != nil {
		r.logger.Error(err, "Failure while updatting RevisionProcessingState")
		err = fmt.Errorf("update RevisionProcessingState: %w", err)
		return ctrl.Result{}, fmt.Errorf("update MRT queueRef: %w", err)
	}
	err = r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.StateProcessingRevision)

	if err != nil {
		r.logger.Info("DEBUG: Unsuccessfull handleSubStatePreflightCheck")
	} else {
		r.logger.Info("DEBUG: Successfull handleSubStatePreflightCheck")
	}

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

	// Verify, if revision was already processed. // TODO: should be removed and replaced with MSR after last MCA check
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

	// Revision should be after last processed MSR revision, to be processed
	msr, err := r.getMSR(ctx, mrt)
	if err != nil {
		return false, "", fmt.Errorf("get MSR for MRT: %w", err)
	}
	if notAfter, err := r.repository(ctx, mrt).IsNotAfter(ctx, msr.Spec.CommitSHA, revision); err != nil {
		r.logger.Error(err, "Failed to check if revision is after last MSR revision", "revision", revision, "msrRevision", msr.Spec.CommitSHA)
		return false, "", fmt.Errorf("check if revision is after last MSR revision")
	} else if notAfter {
		return true, "revision commes after last processed MSR", nil
	}

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
		return ctrl.Result{}, fmt.Errorf("perform ManifestSigningRequest update: %w", err)
	} else if !continueMSR {
		r.logger.Info("Skipping revision since there is no govern files in revision", "revision", revision)
		// Pop the revision from the queue and reset the state to EmptyPending to check the next one.
		if err := r.removeRevisionsQueueHead(ctx, mrt); err != nil {
			return ctrl.Result{}, err
		}

		err := r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.EmptyActionState)
		return ctrl.Result{}, err
	}

	// Revision passes. Transition RevisionProcessingState to StateRevisionAfterMSRUpdate.
	mrt.Status.RevisionProcessingState = governancev1alpha1.StateRevisionAfterMSRUpdate
	if err := r.Status().Update(ctx, mrt); err != nil {
		r.logger.Error(err, "Failure while updatting RevisionProcessingState")
		err = fmt.Errorf("update RevisionProcessingState: %w", err)
		return ctrl.Result{}, err
	}

	err = r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.StateProcessingRevision)
	if err != nil {
		r.logger.Info("DEBUG: Unsuccessfull handleSubStateUpdateMSR")
	} else {
		r.logger.Info("DEBUG: Successfull handleSubStateUpdateMSR")
	}
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

// handleSubstateFinish is responsible for releasing lock (set in previous function) and
// updating MRT's LastObservedCommitHash, RevisionsQueue and setting ActionState, RevisionProcessingState to empty states.
func (r *ManifestRequestTemplateReconciler) handleSubstateFinish(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	revision string,
) (ctrl.Result, error) {
	// Update MRT LastObservedCommitHash and pop the revision from the queue.
	mrt.Status.LastObservedCommitHash = revision
	// Set RevisionProcessingState to an empty state.
	mrt.Status.RevisionProcessingState = governancev1alpha1.StateRevisionEmpty
	if err := r.Status().Update(ctx, mrt); err != nil {
		r.logger.Error(err, "Failure while updatting RevisionProcessingState")
		err = fmt.Errorf("update RevisionProcessingState: %w", err)
		return ctrl.Result{}, err
	}

	// Release lock on success and free the state.
	err := r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.EmptyActionState)

	if err != nil {
		r.logger.Info("DEBUG: Unsuccessfull handleSubstateFinish")
		return ctrl.Result{}, err
	} else {
		r.logger.Info("DEBUG: Successfull handleSubstateFinish")
	}

	// Delete the revision event.
	if err := r.removeRevisionsQueueHead(ctx, mrt); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// checkDependencies validates that all linked resources for an MRT exist.
func (r *ManifestRequestTemplateReconciler) checkDependencies(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) error {
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

// acquireLock sets in cluster MRT ActionState and Condition.Progressing to True, if it isn't set yet.
func (r *ManifestRequestTemplateReconciler) acquireLock(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	newState governancev1alpha1.ActionState,
	message string,
) (bool, error) {
	// Re-fetch is crucial to avoid "object modified" errors
	if err := r.Get(ctx, client.ObjectKeyFromObject(mrt), mrt); err != nil {
		return false, fmt.Errorf("fetch fresh ManifestRequestTemplate: %w", err)
	}

	// Check, if Condition.Progressing was already set.
	if mrt.Status.ActionState == newState && meta.IsStatusConditionTrue(mrt.Status.Conditions, governancev1alpha1.Progressing) {
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
	// The passed 'mrt' should already be the latest version. Don't do Get.

	mrt.Status.ActionState = nextState
	meta.SetStatusCondition(&mrt.Status.Conditions, metav1.Condition{
		Type: governancev1alpha1.Progressing, Status: metav1.ConditionFalse, Reason: reason, Message: message,
	})

	return r.Status().Update(ctx, mrt)
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
