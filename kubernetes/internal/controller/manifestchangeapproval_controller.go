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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestchangeapproval,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestchangeapproval/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestchangeapproval/finalizers,verbs=update
func (r *ManifestChangeApprovalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = logf.FromContext(ctx).WithValues("name", req.Name, "namespace", req.Namespace)
	r.logger.Info("Reconciling ManifestChangeApproval")

	// Fetch the MCA instance
	mca := &governancev1alpha1.ManifestChangeApproval{}
	if err := r.Get(ctx, req.NamespacedName, mca); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetch ManifestChangeApproval for reconcile request: %w", err)
	}

	// Finalize object, if it's being deleted.
	// Object is being deleted, if it contains DeletionTimestamp.
	if !mca.ObjectMeta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.finzalize(ctx, mca)
	}
	
	if _, err := r.repositoryWithError(ctx, mca); err != nil {
		logger.Error(err, "Failed on first repository fetch")
		return ctrl.Result{}, fmt.Errorf("init repo for ManifestChangeApproval: %w", err)
	}

	// Check for `initial` finalize annotation. If the finalizer isn't set yet, then it's a new resource.
	// Do on creation actions.
	if !controllerutil.ContainsFinalizer(mca, GovernanceFinalizer) {
		return ctrl.Result{}, r.onMCACreation(ctx, mca, req)
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

// finalize is used for object clean-up on deletion event.
// So far, it's needed to remove the `initial` finalize annotation.
func (r *ManifestChangeApprovalReconciler) finzalize(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) error {
if !controllerutil.ContainsFinalizer(mca, GovernanceFinalizer) {
		// No custom finalizer is found. Do nothing
		return nil
	}

	// No real clean-up logic is needed
	r.logger.Info("Successfully finalized ManifestChangeApproval")

	// Remove the custom finalizer. The object will be deleted
	controllerutil.RemoveFinalizer(mca, GovernanceFinalizer)
	if err := r.Update(ctx, mca); err != nil {
		return fmt.Errorf("remove finalizer %s: %w", GovernanceFinalizer, err)
	}

	return nil
}

func (r *ManifestChangeApprovalReconciler) onMCACreation(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval, req ctrl.Request) error { // TODO: be transactional
	r.logger.Info("Initializing new ManifestChangeApproval")

	if err := r.saveInRepository(ctx, mca, req); err != nil {
		// If setup fails, we return the error to retry. We don't add the finalizer yet
		r.logger.Error(err, "Failed to save initial ManifestChangeApproval in repository")
		return fmt.Errorf("save initial ManifestChangeApproval in repository: %w", err)
	}

	if err := r.saveNewHistoryRecord(ctx, mca); err != nil {
		r.logger.Error(err, "Failed to save history record for initial ManifestChangeApproval")
		return fmt.Errorf("save history record for initial ManifestChangeApproval")
	}

	// Mark MCA as set up. Take new MCA
	mca = &governancev1alpha1.ManifestChangeApproval{}
	if err := r.Get(ctx, req.NamespacedName, mca); err != nil {
		return fmt.Errorf("fetch ManifestChangeApproval for reconcile request: %w", err)
	}
	controllerutil.AddFinalizer(mca, GovernanceFinalizer)
	if err := r.Update(ctx, mca); err != nil {
		return fmt.Errorf("add finalizer: %w", err)
	}

	r.logger.Info("Successfully finalized ManifestChangeApproval")

	return nil
}

func (r *ManifestChangeApprovalReconciler) saveInRepository(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval, req ctrl.Request) error {
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
