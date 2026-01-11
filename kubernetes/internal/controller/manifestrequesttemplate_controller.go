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
	"sigs.k8s.io/controller-runtime/pkg/builder"
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
	GovernanceFinalizer                 = "governance.nazar.grynko.com/finalizer"
	QubmangoMRTCreationCommitAnnotation = "governance.nazar.grynko.com/mrt-creation-commit-sha"
	QubmangoGovernanceFolder            = ".qubmango"
	QubmangoGovernanceAlias             = "qubmango"
	MRTQueuePrefix                      = "queue-"
	GitPollInterval                     = 5 * time.Minute
)

type RepositoryManager interface {
	GetProviderForMRT(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (repomanager.GitRepository, error)
}

// MRTStateHandler defines a function that performs work within a state and returns the next state
type MRTStateHandler func(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (governancev1alpha1.MRTActionState, error)

// MRTRevisionStateHandler defines a function that performs work within a revision processing state
// and returns the next revision processing state
type MRTRevisionStateHandler func(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, revision string) (governancev1alpha1.MRTNewRevisionState, error)

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
		For(&governancev1alpha1.ManifestRequestTemplate{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			&governancev1alpha1.GovernanceQueue{},
			handler.EnqueueRequestsFromMapFunc(r.findMRTForQueue),
		).
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
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=governanceevents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=governanceevents/status,verbs=get;update;patch

func (r *ManifestRequestTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = log.FromContext(ctx).WithValues("controller", "ManifestRequestTemplate", "name", req.Name, "namespace", req.Namespace)
	r.logger.Info("Starting reconciliation", "mrt", req.NamespacedName)

	// Fetch the MRT instance
	mrt := &governancev1alpha1.ManifestRequestTemplate{}
	if err := r.Get(ctx, req.NamespacedName, mrt); err != nil {
		r.logger.V(2).Info("MRT not found, may have been deleted")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Release lock if hold too long (deadlock prevention)
	if r.isLockForMoreThan(mrt, 30*time.Second) {
		r.logger.Info("Lock held too long, releasing to prevent deadlock", "actionState", mrt.Status.ActionState, "lockDuration", "30s")
		return ctrl.Result{Requeue: true}, r.releaseLockWithFailure(ctx, mrt, mrt.Status.ActionState, fmt.Errorf("lock acquired for too long"))
	}

	// Handle deletion
	if !mrt.ObjectMeta.DeletionTimestamp.IsZero() {
		r.logger.Info("MRT is marked for deletion", "actionState", mrt.Status.ActionState)
		return r.reconcileDelete(ctx, mrt)
	}

	// Handle initialization (before finalizer is set)
	if !controllerutil.ContainsFinalizer(mrt, GovernanceFinalizer) {
		r.logger.Info("MRT not initialized, starting initialization flow", "actionState", mrt.Status.ActionState)
		return r.reconcileCreate(ctx, mrt)
	}

	// Handle normal reconciliation (after initialization)
	r.logger.Info("MRT initialized, processing normal reconciliation", "actionState", mrt.Status.ActionState)
	result, err := r.reconcileNormal(ctx, mrt)

	return r.handleResult(result, err)
}

// withLock wraps state handler logic with lock acquisition and release.
// It provides a clean abstraction for ActionState transitions:
// 1. Acquires lock with re-fetch to prevent conflicts
// 2. Executes the handler function
// 3. On success: releases lock and transitions to nextState
// 4. On failure: releases lock with failure reason and returns error
// This eliminates boilerplate and ensures consistent lock management.
func (r *ManifestRequestTemplateReconciler) withLock(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	state governancev1alpha1.MRTActionState,
	message string,
	handler MRTStateHandler,
) (ctrl.Result, error) {
	lockAcquired, err := r.acquireLock(ctx, mrt, state, message)
	if !lockAcquired || err != nil {
		r.logger.V(2).Info("lock was already acquired, requeuing", "state", state)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	nextState, err := handler(ctx, mrt)
	if err != nil {
		r.logger.Error(err, "Handler failed, releasing lock with failure", "state", state)
		_ = r.releaseLockWithFailure(ctx, mrt, state, err)
		return ctrl.Result{}, err
	}

	r.logger.V(2).Info("Handler succeeded, transitioning state", "from", state, "to", nextState)
	releaseErr := r.releaseLockAndSetNextState(ctx, mrt, nextState)
	return ctrl.Result{Requeue: true}, releaseErr
}

// withRevisionLock wraps revision sub-state handler logic.
// Assumes the outer lock (ActionState) is already held by the parent handler.
// It provides a clean abstraction for RevisionProcessingState transitions:
// 1. Executes the handler function
// 2. On success: updates RevisionProcessingState, releases lock with nextActionState, and requeues
// 3. On failure: releases lock with failure reason and returns error
// The nextActionState parameter allows specifying what ActionState to transition to,
// enabling the last handler to transition back to Empty.
func (r *ManifestRequestTemplateReconciler) withRevisionLock(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	parentState governancev1alpha1.MRTActionState,
	nextActionState governancev1alpha1.MRTActionState,
	revision string,
	handler MRTRevisionStateHandler,
) (ctrl.Result, error) {
	nextRevisionState, err := handler(ctx, mrt, revision)
	if err != nil {
		r.logger.Error(err, "Revision handler failed, releasing lock", "parentState", parentState, "currentRevisionState", mrt.Status.RevisionProcessingState)
		_ = r.releaseLockWithFailure(ctx, mrt, parentState, err)
		return ctrl.Result{}, err
	}

	// Update RevisionProcessingState
	r.logger.V(2).Info("Updating RevisionProcessingState", "from", mrt.Status.RevisionProcessingState, "to", nextRevisionState)
	mrt.Status.RevisionProcessingState = nextRevisionState
	if err := r.Status().Update(ctx, mrt); err != nil {
		r.logger.Error(err, "Failed to update RevisionProcessingState", "newState", nextRevisionState)
		_ = r.releaseLockWithFailure(ctx, mrt, parentState, fmt.Errorf("update RevisionProcessingState: %w", err))
		return ctrl.Result{}, err
	}

	// Transition ActionState to nextActionState and requeue
	releaseErr := r.releaseLockAndSetNextState(ctx, mrt, nextActionState)
	if releaseErr == nil {
		r.logger.V(2).Info("Revision sub-state completed, requeuing", "nextRevisionState", nextRevisionState, "nextActionState", nextActionState)
	}
	return ctrl.Result{Requeue: true}, releaseErr
}

// reconcileDelete handles the cleanup logic when an MRT is being deleted.
func (r *ManifestRequestTemplateReconciler) reconcileDelete(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(mrt, GovernanceFinalizer) {
		// No custom finalizer found, nothing to clean up
		r.logger.V(2).Info("No finalizer found, deletion already in progress")
		return ctrl.Result{}, nil
	}

	r.logger.Info("Processing MRT deletion", "actionState", mrt.Status.ActionState)

	switch mrt.Status.ActionState {
	default:
		// Any state means we start the deletion process
		return r.handleStateDeletion(ctx, mrt)
	}
}

// handleStateDeletion returns ArgoCD Application its original targetRevision and removes finalizer from this resource.
// State: any → EmptyActionState
func (r *ManifestRequestTemplateReconciler) handleStateDeletion(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	return r.withLock(ctx, mrt, governancev1alpha1.MRTActionStateDeletion, "Removing MRT from governance and cluster",
		func(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (governancev1alpha1.MRTActionState, error) {
			// Get the Application
			app, err := r.getApplication(ctx, mrt)
			if err != nil {
				return "", fmt.Errorf("fetch Application for MRT: %w", err)
			}

			// Patch the Application with MCA CommitSHA
			r.logger.Info("Restoring initial ArgoCD Application targetRevision", "app", app.Name, "currRevision", mrt.Status.ApplicationInitTargetRevision, "initialRevision", app.Spec.Source.TargetRevision)

			patch := client.MergeFrom(app.DeepCopy())
			app.Spec.Source.TargetRevision = mrt.Status.ApplicationInitTargetRevision

			if err := r.Patch(ctx, app, patch); err != nil {
				r.logger.Error(err, "Failed to patch ArgoCD Application")
				return "", fmt.Errorf("patch ArgoCD Application targetRevision: %w", err)
			}

			r.logger.Info("ArgoCD Application targetRevision restored successfully")

			// Remove the finalizer from MRT.
			r.logger.Info("Removing GovernanceFinalizer from MRT")
			controllerutil.RemoveFinalizer(mrt, GovernanceFinalizer)
			if err := r.Update(ctx, mrt); err != nil {
				r.logger.Error(err, "Failed to remove finalizer")
				return "", fmt.Errorf("remove finalizer from ManifestRequestTemplate: %w", err)
			}
			r.logger.Info("Deletion complete, finalizer removed")
			return governancev1alpha1.MRTActionStateEmpty, nil
		},
	)
}

func (r *ManifestRequestTemplateReconciler) isLockForMoreThan(
	mrt *governancev1alpha1.ManifestRequestTemplate,
	duration time.Duration,
) bool {
	condition := meta.FindStatusCondition(mrt.Status.Conditions, governancev1alpha1.Progressing)
	return condition != nil && condition.Status == metav1.ConditionTrue && time.Now().Sub(condition.LastTransitionTime.Time) >= duration
}

// reconcileCreate handles the logic for a newly created MRT, that has not been initialized.
// It progresses through these states:
// 1. MRTActionStateSaveArgoCDTargetRevision: Save Application initial targetRevision in MRT Status
// 2. InitStateCreateDefaultClusterResources: Create default MSR/MCA/GovernanceQueue resources
// 3. StateInitSetFinalizer: Set finalizer to complete initialization
func (r *ManifestRequestTemplateReconciler) reconcileCreate(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	r.logger.Info("Initializing new ManifestRequestTemplate", "mrt", mrt.Name, "namespace", mrt.Namespace, "currentState", mrt.Status.ActionState)

	// Check if there is any available git repository provider
	if _, err := r.repositoryWithError(ctx, mrt); err != nil {
		r.logger.Error(err, "Repository provider unavailable, cannot proceed with initialization")
		return ctrl.Result{}, fmt.Errorf("init repo for ManifestRequestTemplate: %w", err)
	}

	switch mrt.Status.ActionState {
	case governancev1alpha1.MRTActionStateEmpty, governancev1alpha1.MRTActionStateSaveArgoCDTargetRevision:
		// 1. Save initial ArgoCD revision for deletion.
		r.logger.V(2).Info("Proceeding to save Application initial targetRevision")
		return r.handleStateSaveArgoCDTargetRevision(ctx, mrt)
	case governancev1alpha1.MRTActionStateCreateDefaultClusterResources:
		// 2. Create default MSR, MCA resources in the cluster.
		r.logger.V(2).Info("Proceeding to create default cluster resources")
		return r.handleInitStateCreateClusterResources(ctx, mrt)
	case governancev1alpha1.MRTActionStateInitSetFinalizer:
		// 3. Confirm MRT initialization by setting the GovernanceFinalizer.
		r.logger.V(2).Info("Proceeding to set finalizer")
		return r.handleStateFinalizing(ctx, mrt)
	default:
		// Any unknown ActionState during initialization - error.
		err := fmt.Errorf("unknown initialization state: %s", string(mrt.Status.ActionState))
		r.logger.Error(err, "Invalid state for initialization")
		return ctrl.Result{}, err
	}
}

// handleStateSaveArgoCDTargetRevision saves Application initial targetRevision in MRT Status.
// State: MRTActionStateSaveArgoCDTargetRevision → MRTActionStateCreateDefaultClusterResources
func (r *ManifestRequestTemplateReconciler) handleStateSaveArgoCDTargetRevision(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	return r.withLock(ctx, mrt, governancev1alpha1.MRTActionStateSaveArgoCDTargetRevision, "Save Application initial targetRevision",
		func(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (governancev1alpha1.MRTActionState, error) {
			r.logger.Info("Saving Application initial targetRevision", "mrt", mrt.Name, "namespace", mrt.Namespace)
			application, err := r.getApplication(ctx, mrt)
			if err != nil {
				return "", fmt.Errorf("fetch Application associated with ManifestRequestTemplate: %w", err)
			}

			mrt.Status.ApplicationInitTargetRevision = application.Spec.Source.TargetRevision
			if err := r.Status().Update(ctx, mrt); err != nil {
				return "", fmt.Errorf("update MRT ApplicationInitTargetRevision: %w", err)
			}

			r.logger.Info("Application initial targetRevision saved successfully")
			return governancev1alpha1.MRTActionStateCreateDefaultClusterResources, nil
		},
	)
}

// handleInitStateCreateClusterResources creates default MSR/MCA/GovernanceQueue resources.
// State: InitStateCreateDefaultClusterResources → StateInitSetFinalizer
func (r *ManifestRequestTemplateReconciler) handleInitStateCreateClusterResources(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	return r.withLock(ctx, mrt, governancev1alpha1.MRTActionStateCreateDefaultClusterResources, "Creating MSR/MCA/GovernanceQueue in cluster",
		func(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (governancev1alpha1.MRTActionState, error) {
			r.logger.Info("Creating default cluster resources", "mrt", mrt.Name, "namespace", mrt.Namespace)
			if err := r.createLinkedDefaultResources(ctx, mrt); err != nil {
				r.logger.Error(err, "Failed to create linked default resources")
				return "", fmt.Errorf("create linked default resources: %w", err)
			}
			r.logger.Info("Default cluster resources created successfully")
			return governancev1alpha1.MRTActionStateInitSetFinalizer, nil
		},
	)
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

	// Get the creation revision from the annotation.
	revision := mrt.GetAnnotations()[QubmangoMRTCreationCommitAnnotation]
	if revision == "" {
		return fmt.Errorf("missing creation commit annotation on MRT during creation")
	}

	// Fetch all changed files in the repository, that where created before.
	fileChanges, _, err := r.repository(ctx, mrt).GetChangedFiles(ctx, "", revision, application.Spec.Source.Path)
	if err != nil {
		return fmt.Errorf("fetch changes between init commit and %s: %w", revision, err)
	}

	// Build default MSR out of MRT
	msr := r.buildInitialMSR(mrt, application, fileChanges, revision)
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
	mca := r.buildInitialMCA(mrt, msr, application, fileChanges, revision)
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
	if err := ctrl.SetControllerReference(mrt, queue, r.Scheme); err != nil {
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

// handleStateFinalizing sets the GovernanceFinalizer on the MRT to complete initialization.
// State: StateInitSetFinalizer → EmptyActionState
func (r *ManifestRequestTemplateReconciler) handleStateFinalizing(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	return r.withLock(ctx, mrt, governancev1alpha1.MRTActionStateInitSetFinalizer, "Setting the GovernanceFinalizer on MRT",
		func(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (governancev1alpha1.MRTActionState, error) {
			r.logger.Info("Adding GovernanceFinalizer to complete initialization")
			controllerutil.AddFinalizer(mrt, GovernanceFinalizer)
			if err := r.Update(ctx, mrt); err != nil {
				r.logger.Error(err, "Failed to add finalizer")
				return "", fmt.Errorf("add finalizer in initialization: %w", err)
			}
			r.logger.Info("Initialization complete, finalizer added")
			return governancev1alpha1.MRTActionStateEmpty, nil
		},
	)
}

// handleResult acts as a middleware to ensure polling is applied on success
func (r *ManifestRequestTemplateReconciler) handleResult(
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

func (r *ManifestRequestTemplateReconciler) buildInitialMSR(
	mrt *governancev1alpha1.ManifestRequestTemplate,
	application *argocdv1alpha1.Application,
	fileChanges []governancev1alpha1.FileChange,
	revision string,
) *governancev1alpha1.ManifestSigningRequest {
	mrtCopy := *mrt.DeepCopy()

	return &governancev1alpha1.ManifestSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mrtCopy.Spec.MSR.Name,
			Namespace: mrtCopy.Spec.MSR.Namespace,
		},
		Spec: governancev1alpha1.ManifestSigningRequestSpec{
			Version:           0,
			CommitSHA:         revision,
			PreviousCommitSHA: "",
			MRT: governancev1alpha1.VersionedManifestRef{
				Name:      mrtCopy.ObjectMeta.Name,
				Namespace: mrtCopy.ObjectMeta.Namespace,
				Version:   mrtCopy.Spec.Version,
			},
			PublicKey:        mrt.Spec.PGP.PublicKey,
			GitRepositoryURL: mrt.Spec.GitRepository.SSH.URL,
			Locations: governancev1alpha1.Locations{
				GovernancePath: filepath.Join(mrt.Spec.GovernanceFolderPath, QubmangoGovernanceFolder),
				SourcePath:     application.Spec.Source.Path,
			},
			Changes: fileChanges,
			Governors: governancev1alpha1.GovernorList{
				Members: []governancev1alpha1.Governor{
					{Alias: QubmangoGovernanceAlias, PublicKey: mrt.Spec.PGP.PublicKey},
				},
			},
			Require: governancev1alpha1.ApprovalRule{Signer: QubmangoGovernanceAlias},
		},
	}
}

func (r *ManifestRequestTemplateReconciler) buildInitialMCA(
	mrt *governancev1alpha1.ManifestRequestTemplate,
	msr *governancev1alpha1.ManifestSigningRequest,
	application *argocdv1alpha1.Application,
	fileChanges []governancev1alpha1.FileChange,
	revision string,
) *governancev1alpha1.ManifestChangeApproval {
	mrtCopy := *mrt.DeepCopy()
	msrCopy := *msr.DeepCopy()

	return &governancev1alpha1.ManifestChangeApproval{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mrt.Spec.MCA.Name,
			Namespace: mrt.Spec.MCA.Namespace,
		},
		Spec: governancev1alpha1.ManifestChangeApprovalSpec{
			Version:           0,
			CommitSHA:         revision,
			PreviousCommitSHA: "",
			MRT: governancev1alpha1.VersionedManifestRef{
				Name:      mrtCopy.ObjectMeta.Name,
				Namespace: mrtCopy.ObjectMeta.Namespace,
				Version:   mrtCopy.Spec.Version,
			},
			MSR: governancev1alpha1.VersionedManifestRef{
				Name:      msrCopy.ObjectMeta.Name,
				Namespace: msrCopy.ObjectMeta.Namespace,
				Version:   msrCopy.Spec.Version,
			},
			PublicKey:        mrtCopy.Spec.PGP.PublicKey,
			GitRepositoryURL: mrtCopy.Spec.GitRepository.SSH.URL,
			Locations: governancev1alpha1.Locations{
				GovernancePath: filepath.Join(mrt.Spec.GovernanceFolderPath, QubmangoGovernanceFolder),
				SourcePath:     application.Spec.Source.Path,
			},
			Changes: fileChanges,
			Governors: governancev1alpha1.GovernorList{
				Members: []governancev1alpha1.Governor{
					{Alias: QubmangoGovernanceAlias, PublicKey: mrt.Spec.PGP.PublicKey},
				},
			},
			Require:             governancev1alpha1.ApprovalRule{Signer: QubmangoGovernanceAlias},
			CollectedSignatures: []governancev1alpha1.Signature{{Signer: QubmangoGovernanceAlias}},
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
			Name:      MRTQueuePrefix + mrt.Name,
			Namespace: mrt.Namespace,
		},
		Spec: governancev1alpha1.GovernanceQueueSpec{
			MRT: mrtMetaRef,
		},
	}
}

// checkForNewCommits helps to compare Remote HEAD vs Local State
func (r *ManifestRequestTemplateReconciler) checkForNewCommits(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (bool, string, error) {

	// Get Remote HEAD
	remoteHead, err := r.repository(ctx, mrt).GetRemoteHeadCommit(ctx)
	if err != nil {
		return false, "", err
	}

	// Compare with what we last processed
	if remoteHead != mrt.Status.LastObservedCommitHash {
		// Check if this revision is already in the Queue
		isPending, err := QueueContainsRevision(ctx, r.Client, &remoteHead, string(mrt.UID))
		if err != nil {
			return false, "", err
		}
		if isPending {
			return false, "", nil
		}

		return true, remoteHead, nil
	}

	return false, "", nil
}

func (r *ManifestRequestTemplateReconciler) reconcileNormal(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	// Check for active lock
	if meta.IsStatusConditionTrue(mrt.Status.Conditions, governancev1alpha1.Progressing) {
		r.logger.V(2).Info("Waiting for ongoing operation to complete", "actionState", mrt.Status.ActionState)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// If ActionState is empty, decide what action to take
	if mrt.Status.ActionState == governancev1alpha1.MRTActionStateEmpty {
		// Check if there are new revisions in the queue
		newRevision, err := r.anyRevisionEvents(ctx, mrt)
		if err != nil {
			r.logger.Error(err, "Failed to check for revision events")
			return ctrl.Result{}, fmt.Errorf("is any revision event present: %w", err)
		}

		if newRevision {
			// If new revision - start MSR process
			r.logger.Info("New revisions found in queue, starting revision processing")
			return ctrl.Result{Requeue: true}, r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.MRTActionStateNewRevision)
		} else {
			// If no new revision - check for updates in remote repository
			shouldProcess, newCommit, err := r.checkForNewCommits(ctx, mrt)
			if err != nil {
				r.logger.Error(err, "Failed to check git for new commits")
				// Requeue after poll interval to retry
				return ctrl.Result{RequeueAfter: GitPollInterval}, nil
			}

			if shouldProcess {
				r.logger.Info("Detected new commit in Git, starting governance process", "commit", newCommit)

				// Create the Event
				mrtRef := &governancev1alpha1.ManifestRef{Name: mrt.Name, Namespace: mrt.Namespace}
				if err := CreateNewRevisionEvent(ctx, r.Client, &newCommit, mrtRef, string(mrt.UID)); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to create revision event: %w", err)
				}

				return ctrl.Result{Requeue: true}, r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.MRTActionStateNewRevision)
			}
		}

		r.logger.V(3).Info("No new revisions to process")
		return ctrl.Result{}, nil
	}

	// Execute action based on current ActionState
	switch mrt.Status.ActionState {
	case governancev1alpha1.MRTActionStateNewRevision:
		r.logger.V(2).Info("Processing revision")
		return r.handleStateProcessingRevision(ctx, mrt)
	case governancev1alpha1.MRTActionStateEmpty:
		r.logger.V(2).Info("Empty empty state - do nothing", "actionState", mrt.Status.ActionState)
		return ctrl.Result{}, r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.MRTActionStateEmpty)
	default:
		// If the state is unknown - reset to EmptyState
		r.logger.V(2).Info("Unknown action state, resetting", "actionState", mrt.Status.ActionState)
		return ctrl.Result{}, r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.MRTActionStateEmpty)
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

// handleStateProcessingRevision orchestrates processing of a new revision from the queue.
// State: StateProcessingRevision with sub-states:
// 1. MRTNewRevisionStatePreflightCheck: Evaluate if revision should be processed
// 2. MRTNewRevisionStateUpdateMSRSpec: Update MSR with changed files from revision
// 3. MRTNewRevisionStateAfterMSRUpdate: Finalize and remove event from queue
// Final state: MRTActionStateEmpty + MRTNewRevisionStateEmpty
// Abort state: MRTNewRevisionStateAbort
func (r *ManifestRequestTemplateReconciler) handleStateProcessingRevision(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (ctrl.Result, error) {
	r.logger.Info("Starting revision processing", "revisionState", mrt.Status.RevisionProcessingState)

	// Acquire Lock
	lockAcquired, err := r.acquireLock(ctx, mrt, governancev1alpha1.MRTActionStateNewRevision, "Processing new Git revision")
	if !lockAcquired || err != nil {
		r.logger.V(2).Info("Lock already held, requeuing", "revisionState", mrt.Status.RevisionProcessingState)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}

	// Check all dependencies exist
	if err := r.checkDependencies(ctx, mrt); err != nil {
		r.logger.Error(err, "Dependency check failed")
		_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.MRTActionStateNewRevision, err)
		return ctrl.Result{}, fmt.Errorf("check dependency: %w", err)
	}

	// Check repository connection exists
	if _, err := r.repositoryWithError(ctx, mrt); err != nil {
		r.logger.Error(err, "Repository unavailable for revision processing")
		_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.MRTActionStateNewRevision, err)
		return ctrl.Result{}, fmt.Errorf("check repository: %w", err)
	}

	// Handle the race condition gracefully
	newRevision, err := r.anyRevisionEvents(ctx, mrt)
	if err != nil {
		r.logger.Error(err, "Failed to check for revision events")
		_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.MRTActionStateNewRevision, err)
		return ctrl.Result{}, fmt.Errorf("is any revision event present: %w", err)
	}
	if !newRevision {
		r.logger.Info("Queue is empty but state is NewRevision. Assuming successful cleanup race condition.", "currentRevisionState", mrt.Status.RevisionProcessingState)
		return ctrl.Result{Requeue: true}, r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.MRTActionStateEmpty)
	}

	// Get the revision we are working on
	event, err := RevisionsQueueHead(ctx, r.Client, mrt.Status.RevisionQueueRef)
	if err != nil {
		r.logger.Error(err, "Failed to get revision from queue")
		return ctrl.Result{}, fmt.Errorf("get head of the revision queue: %w", err)
	}

	revision := event.Spec.NewRevision.CommitSHA

	// Dispatch to the correct revision sub-state handler
	switch mrt.Status.RevisionProcessingState {
	case governancev1alpha1.MRTNewRevisionStateEmpty, governancev1alpha1.MRTNewRevisionStatePreflightCheck:
		// 1. Decide whether the MSR process should start or revision is worth to skip
		r.logger.V(2).Info("Dispatching to preflight check", "revision", revision)
		return r.handleSubStatePreflightCheck(ctx, mrt, revision)
	case governancev1alpha1.MRTNewRevisionStateUpdateMSRSpec:
		// 2. Update the MSR Spec with data from new revision (changed files, revision hash, etc.)
		r.logger.V(2).Info("Dispatching to MSR update", "revision", revision)
		return r.handleSubStateUpdateMSR(ctx, mrt, revision)
	case governancev1alpha1.MRTNewRevisionStateAfterMSRUpdate:
		// 3. After MSR is updated, finalize and remove event from queue
		r.logger.V(2).Info("Dispatching to finalization", "revision", revision)
		return r.handleSubstateFinish(ctx, mrt, revision)
	case governancev1alpha1.MRTNewRevisionStateAbort:
		r.logger.V(2).Info("Dispatching to abort", "revision", revision)
		mrt.Status.RevisionProcessingState = governancev1alpha1.MRTNewRevisionStateEmpty
		_ = r.releaseLockAndSetNextState(ctx, mrt, governancev1alpha1.MRTActionStateEmpty)
		return ctrl.Result{}, err
	default:
		// Any unknown RevisionProcessingState during state processing - error
		err := fmt.Errorf("unknown RevisionProcessingState state: %s", string(mrt.Status.RevisionProcessingState))
		r.logger.Error(err, "Invalid revision processing state")
		_ = r.releaseLockWithFailure(ctx, mrt, governancev1alpha1.MRTActionStateNewRevision, err)
		return ctrl.Result{}, err
	}
}

// handleSubStatePreflightCheck decides whether the revision should be processed or skipped.
// Sub-state: MRTNewRevisionStatePreflightCheck/MRTNewRevisionStateEmpty → MRTNewRevisionStateUpdateMSRSpec (or EmptyActionState if skip)
func (r *ManifestRequestTemplateReconciler) handleSubStatePreflightCheck(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	revision string,
) (ctrl.Result, error) {
	return r.withRevisionLock(ctx, mrt, governancev1alpha1.MRTActionStateNewRevision, governancev1alpha1.MRTActionStateNewRevision, revision,
		func(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, revision string) (governancev1alpha1.MRTNewRevisionState, error) {
			r.logger.Info("Evaluating revision for processing", "revision", revision)

			// Evaluate the revision and return if it should be skipped
			shouldSkip, reason, err := r.shouldSkipRevision(ctx, mrt, revision)
			if err != nil {
				r.logger.Error(err, "Failed to evaluate revision")
				return "", fmt.Errorf("evaluate new revision from revision queue: %w", err)
			}

			if shouldSkip {
				r.logger.Info("Skipping revision based on pre-flight check", "revision", revision, "reason", reason)
				// Pop the revision from the queue and reset the state to Empty
				if err := RemoveRevisionsQueueHead(ctx, r.Client, mrt.Status.RevisionQueueRef); err != nil {
					r.logger.Error(err, "Failed to remove revision from queue")
					return "", err
				}
				// Transition back to Empty (handled by caller)
				return governancev1alpha1.MRTNewRevisionStateAbort, nil
			}

			r.logger.Info("Revision passed pre-flight check, proceeding to MSR update", "revision", revision)
			return governancev1alpha1.MRTNewRevisionStateUpdateMSRSpec, nil
		},
	)
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

	// Revision should be after last processed MSR revision, to be processed
	msr, err := r.getMSR(ctx, mrt)
	if err != nil {
		return false, "", fmt.Errorf("get MSR for MRT: %w", err)
	}
	if notAfter, err := r.repository(ctx, mrt).IsNotAfter(ctx, msr.Spec.CommitSHA, revision); err != nil {
		r.logger.Error(err, "Failed to check if revision is after last MSR revision", "revision", revision, "msrRevision", msr.Spec.CommitSHA)
		return false, "", fmt.Errorf("check if revision is after last MSR revision")
	} else if notAfter {
		return true, fmt.Sprintf("Revision %s comes before last processed MSR commit %s", revision, msr.Spec.CommitSHA), nil
	}

	return false, "", nil
}

// handleSubStateUpdateMSR updates the MSR Spec with changes from the new revision.
// Sub-state: MRTNewRevisionStateUpdateMSRSpec → MRTNewRevisionStateAfterMSRUpdate (or EmptyActionState if no changes)
func (r *ManifestRequestTemplateReconciler) handleSubStateUpdateMSR(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	revision string,
) (ctrl.Result, error) {
	return r.withRevisionLock(ctx, mrt, governancev1alpha1.MRTActionStateNewRevision, governancev1alpha1.MRTActionStateNewRevision, revision,
		func(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, revision string) (governancev1alpha1.MRTNewRevisionState, error) {
			r.logger.Info("Updating MSR with changes from revision", "revision", revision)

			continueMSR, err := r.performMSRUpdate(ctx, mrt, revision)
			if err != nil {
				r.logger.Error(err, "Failed to update MSR")
				return "", fmt.Errorf("perform ManifestSigningRequest update: %w", err)
			}

			if !continueMSR {
				r.logger.Info("No manifest files changed in revision, skipping MSR processing", "revision", revision)
				// Pop the revision from the queue and reset state to Empty
				if err := RemoveRevisionsQueueHead(ctx, r.Client, mrt.Status.RevisionQueueRef); err != nil {
					r.logger.Error(err, "Failed to remove revision from queue")
					return "", err
				}
				// Transition back to Empty (handled by caller)
				return governancev1alpha1.MRTNewRevisionStateAbort, nil
			}

			r.logger.Info("MSR update complete, ready to finalize", "revision", revision)
			return governancev1alpha1.MRTNewRevisionStateAfterMSRUpdate, nil
		},
	)
}

// performMSRUpdate takes all the changed files for revision.
// If there any govern manifests (manifests files, under ArgoCD controll and not in the governanceFolder / qubmango folder),
// it updates MSR attached to current MRT with new Spec.
// First return parameter reflects, whether MSR should be processed further (true),
// or skipped (false), in case if there is no new changes to govern. In case of error this parameter is false.
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
		r.logger.Error(err, "Failed to fetch ManifestChangeApproval by ManifestRequestTemplate")
		return false, fmt.Errorf("get ManifestChangeApproval for ManifestRequestTemplate: %w", err)
	}
	msr, err := r.getMSR(ctx, mrt)
	if err != nil {
		r.logger.Error(err, "Failed to fetch ManifestSigningRequest by ManifestRequestTemplate")
		return false, fmt.Errorf("get ManifestSigningRequest for ManifestRequestTemplate: %w", err)
	}

	// Get changed alias from git repository.
	changedFiles, _, err := r.repository(ctx, mrt).GetChangedFiles(ctx, mca.Spec.CommitSHA, revision, application.Spec.Source.Path)
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
	} else if r.hasNoNewUpdates(msr, changedFiles) {
		mrt.Status.LastObservedCommitHash = revision
		r.logger.Info("No manifest file changes detected between commits. Skipping MSR creation.")
		return false, nil
	}

	// Created updated version of MSR with higher version
	updatedMSR := r.getMSRWithNewVersion(mrt, msr, application, revision, mca.Spec.CommitSHA, changedFiles)

	// Update MSR spec in cluster. Trigger so push of new MSR to git repository from MSR controller
	if err := r.Update(ctx, updatedMSR); err != nil {
		r.logger.Error(err, "Failed to update MSR spec in cluster after successful revision processed")
		return false, fmt.Errorf("after successful MSR revision processed: %w", err)
	}

	return true, nil
}

// hasNoNewUpdates compares changes between MSR and changedFiles. It returns true, if there is any change between two arrays.
func (r *ManifestRequestTemplateReconciler) hasNoNewUpdates(
	msr *governancev1alpha1.ManifestSigningRequest,
	changedFiles []governancev1alpha1.FileChange,
) bool {
	// Old and new changes have different length
	if len(msr.Spec.Changes) != len(changedFiles) {
		return false
	}

	msrFilesMap := make(map[string]governancev1alpha1.FileChange)
	for _, f := range msr.Spec.Changes {
		msrFilesMap[f.Path] = f
	}

	// Verify, that new changes have at least one new file / changed file and return false.
	for _, f := range changedFiles {
		msrF, exists := msrFilesMap[f.Path]
		if !exists || f != msrF {
			return false
		}
	}

	// No changes noticed - return true.
	return true
}

// handleSubstateFinish completes revision processing and removes the event from the queue.
// Sub-state: MRTNewRevisionStateAfterMSRUpdate → MRTNewRevisionStateEmpty (final)
// Transitions ActionState back to MRTActionStateEmpty, completing the revision processing cycle.
func (r *ManifestRequestTemplateReconciler) handleSubstateFinish(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	revision string,
) (ctrl.Result, error) {
	return r.withRevisionLock(ctx, mrt, governancev1alpha1.MRTActionStateNewRevision, governancev1alpha1.MRTActionStateEmpty, revision,
		func(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, revision string) (governancev1alpha1.MRTNewRevisionState, error) {
			r.logger.Info("Finalizing revision processing", "revision", revision)

			// Update LastObservedCommitHash
			mrt.Status.LastObservedCommitHash = revision

			// Delete the revision event from queue
			r.logger.Info("Removing revision event from queue")
			if err := RemoveRevisionsQueueHead(ctx, r.Client, mrt.Status.RevisionQueueRef); err != nil {
				r.logger.Error(err, "Failed to remove revision from queue")
				return "", err
			}

			r.logger.Info("Revision processing complete", "revision", revision)
			return governancev1alpha1.MRTNewRevisionStateEmpty, nil
		},
	)
}

// checkDependencies validates that all linked resources for an MRT exist.
func (r *ManifestRequestTemplateReconciler) checkDependencies(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) error {
	// Check for Application
	app := &argocdv1alpha1.Application{}
	err := r.Get(ctx, types.NamespacedName{Name: mrt.Spec.ArgoCD.Application.Name, Namespace: mrt.Spec.ArgoCD.Application.Namespace}, app)
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

	// Check for GovernanceQueue
	queue := &governancev1alpha1.GovernanceQueue{}
	err = r.Get(ctx, types.NamespacedName{Name: mrt.Status.RevisionQueueRef.Name, Namespace: mrt.Status.RevisionQueueRef.Namespace}, queue)
	if err != nil {
		r.logger.Error(err, "Failed to find linked GovernanceQueue")
		return fmt.Errorf("couldn't find linked GovernanceQueue: %w", err)
	}

	return nil
}

func (r *ManifestRequestTemplateReconciler) getMSRWithNewVersion(
	mrt *governancev1alpha1.ManifestRequestTemplate,
	msr *governancev1alpha1.ManifestSigningRequest,
	application *argocdv1alpha1.Application,
	revision string,
	prevRevision string,
	changedFiles []governancev1alpha1.FileChange,
) *governancev1alpha1.ManifestSigningRequest {
	mrtSpecCpy := mrt.Spec.DeepCopy()
	updatedMSR := msr.DeepCopy()

	updatedMSR.Spec.Version = updatedMSR.Spec.Version + 1
	updatedMSR.Spec.CommitSHA = revision
	updatedMSR.Spec.PreviousCommitSHA = prevRevision
	updatedMSR.Spec.MRT = governancev1alpha1.VersionedManifestRef{
		Name:      mrt.ObjectMeta.Name,
		Namespace: mrt.ObjectMeta.Namespace,
		Version:   mrt.Spec.Version,
	}
	updatedMSR.Spec.PublicKey = mrtSpecCpy.PGP.PublicKey
	updatedMSR.Spec.GitRepositoryURL = mrt.Spec.GitRepository.SSH.URL
	updatedMSR.Spec.Locations = governancev1alpha1.Locations{
		GovernancePath: filepath.Join(mrtSpecCpy.GovernanceFolderPath, QubmangoGovernanceFolder),
		SourcePath:     application.Spec.Source.Path,
	}
	updatedMSR.Spec.Changes = changedFiles
	updatedMSR.Spec.Governors = mrtSpecCpy.Governors
	updatedMSR.Spec.Require = mrtSpecCpy.Require

	return updatedMSR
}

func (r *ManifestRequestTemplateReconciler) filterNonManifestFiles(
	files []governancev1alpha1.FileChange,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) []governancev1alpha1.FileChange {

	var filtered []governancev1alpha1.FileChange

	// normalize paths
	governanceFolder := filepath.Clean(filepath.Join(mrt.Spec.GovernanceFolderPath, QubmangoGovernanceFolder))

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

		if isGovernanceFile {
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
	newState governancev1alpha1.MRTActionState,
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
	nextState governancev1alpha1.MRTActionState,
	cause error,
) error {
	return r.releaseLockAbstract(ctx, mrt, nextState, "StepFailed", fmt.Sprintf("Error occurred: %v", cause))
}

func (r *ManifestRequestTemplateReconciler) releaseLockAndSetNextState(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	nextState governancev1alpha1.MRTActionState,
) error {
	return r.releaseLockAbstract(ctx, mrt, nextState, "StepComplete", "Step completed, proceeding to next state")
}

func (r *ManifestRequestTemplateReconciler) releaseLockAbstract(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	nextState governancev1alpha1.MRTActionState,
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
		Name:      mrt.Spec.ArgoCD.Application.Name,
		Namespace: mrt.Spec.ArgoCD.Application.Namespace,
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
