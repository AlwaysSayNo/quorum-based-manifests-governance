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

// ManifestSigningRequestReconciler reconciles a ManifestSigningRequest object
type ManifestSigningRequestReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	RepoManager RepositoryManager
	logger      logr.Logger
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManifestSigningRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&governancev1alpha1.ManifestSigningRequest{}).
		Named("manifestsigningrequest").
		Complete(r)
}

// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestsigningrequests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestsigningrequests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestsigningrequests/finalizers,verbs=update
func (r *ManifestSigningRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = logf.FromContext(ctx).WithValues("name", req.Name, "namespace", req.Namespace)
	r.logger.Info("Reconciling ManifestSigningRequest")

	// Fetch the MSR instance
	msr := &governancev1alpha1.ManifestSigningRequest{}
	if err := r.Get(ctx, req.NamespacedName, msr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetch ManifestSigningRequest for reconcile request: %w", err)
	}

	// Finalize object, if it's being deleted.
	// Object is being deleted, if it contains DeletionTimestamp.
	if !msr.ObjectMeta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.finzalize(ctx, msr)
	}

	if _, err := r.repositoryWithError(ctx, msr); err != nil {
		logger.Error(err, "Failed on first repository fetch")
		return ctrl.Result{}, fmt.Errorf("init repo for ManifestSigningRequest: %w", err)
	}

	// Check for `initial` finalize annotation. If the finalizer isn't set yet, then it's a new resource.
	// Do on creation actions.
	if !controllerutil.ContainsFinalizer(msr, GovernanceFinalizer) {
		return ctrl.Result{}, r.onMSRCreation(ctx, msr, req)
	}

	// Get the latest history record version
	latestHistoryVersion := -1
	history := msr.Status.RequestHistory
	if len(history) > 0 {
		latestHistoryVersion = history[len(history)-1].Version
	}

	// If the spec's version is newer - reconcile status
	if msr.Spec.Version > latestHistoryVersion {
		r.logger.Info("Detected new version in spec, updating status history", "specVersion", msr.Spec.Version, "statusVersion", latestHistoryVersion)
		return ctrl.Result{}, r.saveNewHistoryRecord(ctx, msr)
	}

	// TODO: Signature Observer logic

	return ctrl.Result{}, nil
}

// finalize is used for object clean-up on deletion event.
// So far, it's needed to remove the `initial` finalize annotation.
func (r *ManifestSigningRequestReconciler) finzalize(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) error {
	if !controllerutil.ContainsFinalizer(msr, GovernanceFinalizer) {
		// No custom finalizer is found. Do nothing
		return nil
	}

	// No real clean-up logic is needed
	r.logger.Info("Successfully finalized ManifestSigningRequest")

	// Remove the custom finalizer. The object will be deleted
	controllerutil.RemoveFinalizer(msr, GovernanceFinalizer)
	if err := r.Update(ctx, msr); err != nil {
		return fmt.Errorf("remove finalizer %s: %w", GovernanceFinalizer, err)
	}

	return nil
}

func (r *ManifestSigningRequestReconciler) onMSRCreation(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest, req ctrl.Request) error { // TODO: be transactional
	r.logger.Info("Initializing new ManifestSigningRequest")

	if err := r.saveInRepository(ctx, msr, req); err != nil {
		// If setup fails, we return the error to retry. We don't add the finalizer yet
		r.logger.Error(err, "Failed to save initial ManifestSigningRequest in repository")
		return fmt.Errorf("save initial ManifestSigningRequest in repository: %w", err)
	}

	if err := r.saveNewHistoryRecord(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to save history record for initial ManifestSigningRequest")
		return fmt.Errorf("save history record for initial ManifestSigningRequest")
	}

	// Mark MSR as set up. Take new MSR
	msr = &governancev1alpha1.ManifestSigningRequest{}
	if err := r.Get(ctx, req.NamespacedName, msr); err != nil {
		return fmt.Errorf("fetch ManifestSigningRequest for reconcile request: %w", err)
	}
	controllerutil.AddFinalizer(msr, GovernanceFinalizer)
	if err := r.Update(ctx, msr); err != nil {
		return fmt.Errorf("add finalizer: %w", err)
	}

	r.logger.Info("Successfully finalized ManifestSigningRequest")

	return nil
}

func (r *ManifestSigningRequestReconciler) saveInRepository(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest, req ctrl.Request) error {
	repositoryMSR := r.createRepositoryMSR(msr)
	if _, err := r.repository(ctx, msr).PushMSR(ctx, &repositoryMSR); err != nil {
		r.logger.Error(err, "Failed to push initial ManifestSigningRequest in repository")
		return fmt.Errorf("push initial ManifestSigningRequest to repository: %w", err)
	}

	return nil
}

func (r *ManifestSigningRequestReconciler) createRepositoryMSR(msr *governancev1alpha1.ManifestSigningRequest) governancev1alpha1.ManifestSigningRequestManifestObject {
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

func (r *ManifestSigningRequestReconciler) saveNewHistoryRecord(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) error {
	// Append a new history record
	newRecord := r.createNewMSRHistoryRecordFromSpec(&msr.Spec)
	msr.Status.RequestHistory = append(msr.Status.RequestHistory, newRecord)

	// Save the change
	if err := r.Status().Update(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to update MSR status with new history record")
		return fmt.Errorf("update ManifestSigningRequest status with new history record: %w", err)
	}

	r.logger.Info("Successfully added new MSR record to status")
	return nil
}

func (r *ManifestSigningRequestReconciler) createNewMSRHistoryRecordFromSpec(spec *governancev1alpha1.ManifestSigningRequestSpec) governancev1alpha1.ManifestSigningRequestHistoryRecord {
	return governancev1alpha1.ManifestSigningRequestHistoryRecord{
		Version:   spec.Version,
		Changes:   spec.Changes,
		Governors: spec.Governors,
		Require:   spec.Require,
		Approves: governancev1alpha1.ApproverList{
			Members: []governancev1alpha1.Governor{},
		},
		Status: spec.Status,
	}
}

func (r *ManifestSigningRequestReconciler) getExistingMRTForMSR(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (*governancev1alpha1.ManifestRequestTemplate, error) {
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

func (r *ManifestSigningRequestReconciler) repositoryWithError(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (repomanager.GitRepository, error) {
	mrt, err := r.getExistingMRTForMSR(ctx, msr)
	if err != nil {
		return nil, fmt.Errorf("get ManifestRequestTemplate for repository: %w", err)
	}

	return r.RepoManager.GetProviderForMRT(ctx, mrt)
}

func (r *ManifestSigningRequestReconciler) repository(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) repomanager.GitRepository {
	mrt, _ := r.getExistingMRTForMSR(ctx, msr)
	repo, _ := r.RepoManager.GetProviderForMRT(ctx, mrt)
	return repo
}
