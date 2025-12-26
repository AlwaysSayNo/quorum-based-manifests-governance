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

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	repomanager "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository"
)

// ManifestChangeApprovalReconciler reconciles a ManifestChangeApproval object
type ManifestChangeApprovalReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	RepoManager RepositoryManager
	logger      logr.Logger
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManifestChangeApprovalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&governancev1alpha1.ManifestChangeApproval{}).
		Named("manifestchangeapproval").
		Complete(r)
}

// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestchangeapprovals,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestchangeapprovals/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestchangeapprovals/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

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

	// Handle initialization
	if !controllerutil.ContainsFinalizer(mca, GovernanceFinalizer) {
		return r.reconcileCreate(ctx, mca, req)
	}

	// Handle normal reconciliation
	return r.reconcileNormal(ctx, mca, req)
}

// reconcileCreate handles the logic for a newly created MCA that has not been initialized.
func (r *ManifestChangeApprovalReconciler) reconcileDelete(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) (ctrl.Result, error) {
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

// reconcileCreate handles the logic for a newly created MCA that has not been initialized.
func (r *ManifestChangeApprovalReconciler) reconcileCreate(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval, req ctrl.Request) (ctrl.Result, error) { // TODO: be transactional
	r.logger.Info("Initializing new ManifestChangeApproval")

	// Check if MCA is being initialized
	if meta.IsStatusConditionTrue(mca.Status.Conditions, governancev1alpha1.Progressing) {
		r.logger.Info("Initialization is already in progress. Waiting")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// Acquire the lock
	r.logger.Info("Setting Progressing=True to begin initialization")
	meta.SetStatusCondition(&mca.Status.Conditions, metav1.Condition{
		Type:    governancev1alpha1.Progressing,
		Status:  metav1.ConditionTrue,
		Reason:  "CreatingInitialState",
		Message: "Creating default history entry and performing initial Git commit",
	})
	if err := r.Status().Update(ctx, mca); err != nil {
		r.logger.Error(err, "Failed to set Progressing condition")
		return ctrl.Result{}, err
	}

	// Check repository connection
	if _, err := r.repositoryWithError(ctx, mca); err != nil {
		r.logger.Error(err, "Failed to connect to repository.")
		// Release lock with reason failed
		meta.SetStatusCondition(&mca.Status.Conditions, metav1.Condition{
			Type:    governancev1alpha1.Progressing,
			Status:  metav1.ConditionFalse,
			Reason:  "InitializationFailed",
			Message: err.Error(),
		})
		// Also mark Available as false
		meta.SetStatusCondition(&mca.Status.Conditions, metav1.Condition{
			Type:    governancev1alpha1.Available,
			Status:  metav1.ConditionFalse,
			Reason:  "InitializationFailed",
			Message: err.Error(),
		})

		freshMCA := &governancev1alpha1.ManifestChangeApproval{}
		_ = r.Get(ctx, req.NamespacedName, freshMCA)
		freshMCA.Status.Conditions = mca.Status.Conditions
		_ = r.Status().Update(ctx, freshMCA)

		return ctrl.Result{RequeueAfter: 30 * time.Second}, fmt.Errorf("init repo for ManifestChangeApproval: %w", err)
	}

	r.logger.Info("Start saving initial MCA in repository")
	if err := r.saveInRepository(ctx, mca); err != nil {
		r.logger.Error(err, "Failed to save initial ManifestChangeApproval in repository")
		// Release lock with reason failed
		meta.SetStatusCondition(&mca.Status.Conditions, metav1.Condition{
			Type:    governancev1alpha1.Progressing,
			Status:  metav1.ConditionFalse,
			Reason:  "InitializationFailed",
			Message: err.Error(),
		})
		// Also mark Available as false
		meta.SetStatusCondition(&mca.Status.Conditions, metav1.Condition{
			Type:    governancev1alpha1.Available,
			Status:  metav1.ConditionFalse,
			Reason:  "InitializationFailed",
			Message: err.Error(),
		})

		freshMCA := &governancev1alpha1.ManifestChangeApproval{}
		_ = r.Get(ctx, req.NamespacedName, freshMCA)
		freshMCA.Status.Conditions = mca.Status.Conditions
		_ = r.Status().Update(ctx, freshMCA)

		return ctrl.Result{}, fmt.Errorf("save initial ManifestChangeApproval in repository: %w", err)
	}
	r.logger.Info("Finish saving initial MCA in repository")

	// Mark MCA as set up. Take new MCA
	r.logger.Info("Start setting finalizer on the MCA")
	latestMCA := &governancev1alpha1.ManifestChangeApproval{}
	if err := r.Get(ctx, req.NamespacedName, latestMCA); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to re-fetch MCA before finalization: %w", err)
	}

	// Add new initial history record
	newRecord := r.createNewMCAHistoryRecordFromSpec(mca)
	latestMCA.Status.ApprovalHistory = append(latestMCA.Status.ApprovalHistory, newRecord)
	// Add finalizer
	controllerutil.AddFinalizer(mca, GovernanceFinalizer)

	// Update status conditions to reflect success
	meta.SetStatusCondition(&latestMCA.Status.Conditions, metav1.Condition{
		Type:    governancev1alpha1.Progressing,
		Status:  metav1.ConditionFalse,
		Reason:  "InitializationSuccessful",
		Message: "Initial state successfully committed to Git and cluster",
	})
	meta.SetStatusCondition(&latestMCA.Status.Conditions, metav1.Condition{
		Type:    governancev1alpha1.Available,
		Status:  metav1.ConditionTrue,
		Reason:  "SetupComplete",
		Message: "Governance is active for this MCA",
	})

	if err := r.Status().Update(ctx, mca); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply finalizer and set final status: %w", err)
	}
	controllerutil.AddFinalizer(mca, GovernanceFinalizer)
	if err := r.Update(ctx, mca); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply finalizer and set final status: %w", err)
	}
	r.logger.Info("Finish setting finalizer on the MCA")

	r.logger.Info("Successfully finalized ManifestChangeApproval")
	return ctrl.Result{}, nil
}

func (r *ManifestChangeApprovalReconciler) reconcileNormal(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval, req ctrl.Request) (ctrl.Result, error) {
	if meta.IsStatusConditionTrue(mca.Status.Conditions, governancev1alpha1.Progressing) {
		r.logger.Info("Waiting for ongoing operation to complete.")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// Get the latest history record version
	latestHistoryVersion := -1
	history := mca.Status.ApprovalHistory
	if len(history) > 0 {
		latestHistoryVersion = history[len(history)-1].Version
	}

	// If the spec's version is newer - reconcile status
	if mca.Spec.Version > latestHistoryVersion {
		r.logger.Info("Detected new version in spec, updating status history", "specVersion", mca.Spec.Version, "statusVersion", latestHistoryVersion)
		return ctrl.Result{}, r.saveNewHistoryRecord(ctx, mca)
	}

	return ctrl.Result{}, nil
}

func (r *ManifestChangeApprovalReconciler) saveInRepository(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) error {
	repositoryMCA := r.createRepositoryMCA(mca)
	if _, err := r.repository(ctx, mca).PushMCA(ctx, &repositoryMCA); err != nil {
		r.logger.Error(err, "Failed to push initial ManifestChangeApproval in repository")
		return fmt.Errorf("push initial ManifestChangeApproval to repository: %w", err)
	}

	return nil
}

func (r *ManifestChangeApprovalReconciler) createRepositoryMCA(mca *governancev1alpha1.ManifestChangeApproval) governancev1alpha1.ManifestChangeApprovalManifestObject {
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

func (r *ManifestChangeApprovalReconciler) saveNewHistoryRecord(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) error {
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

func (r *ManifestChangeApprovalReconciler) createNewMCAHistoryRecordFromSpec(mca *governancev1alpha1.ManifestChangeApproval) governancev1alpha1.ManifestChangeApprovalHistoryRecord {
	mcaCopy := mca.DeepCopy()

	return governancev1alpha1.ManifestChangeApprovalHistoryRecord{
		CommitSHA: mcaCopy.Spec.LastApprovedCommitSHA,
		Time:      metav1.NewTime(time.Now()),
		Version:   mcaCopy.Spec.Version,
		Changes:   mcaCopy.Spec.Changes,
		Governors: mcaCopy.Spec.Governors,
		Require:   mcaCopy.Spec.Require,
		Approves:  mcaCopy.Status.Approves,
	}
}

func (r *ManifestChangeApprovalReconciler) getExistingMRTForMCA(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) (*governancev1alpha1.ManifestRequestTemplate, error) {
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

func (r *ManifestChangeApprovalReconciler) repositoryWithError(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) (repomanager.GitRepository, error) {
	mrt, err := r.getExistingMRTForMCA(ctx, mca)
	if err != nil {
		return nil, fmt.Errorf("get ManifestRequestTemplate for repository: %w", err)
	}

	return r.RepoManager.GetProviderForMRT(ctx, mrt)
}

func (r *ManifestChangeApprovalReconciler) repository(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) repomanager.GitRepository {
	mrt, _ := r.getExistingMRTForMCA(ctx, mca)
	repo, _ := r.RepoManager.GetProviderForMRT(ctx, mrt)
	return repo
}
