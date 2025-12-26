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
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if !msr.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, msr)
	}

	// Handle initialization
	if !controllerutil.ContainsFinalizer(msr, GovernanceFinalizer) {
		return r.reconcileCreate(ctx, msr, req)
	}

	// Handle normal reconciliation
	return r.reconcileNormal(ctx, msr, req)
}

// reconcileDelete handles the cleanup logic when an MSR is being deleted.
func (r *ManifestSigningRequestReconciler) reconcileDelete(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(msr, GovernanceFinalizer) {
		// No custom finalizer is found. Do nothing
		return ctrl.Result{}, nil
	}

	// No real clean-up logic is needed
	r.logger.Info("Successfully finalized ManifestSigningRequest")

	// Remove the custom finalizer. The object will be deleted
	controllerutil.RemoveFinalizer(msr, GovernanceFinalizer)
	if err := r.Update(ctx, msr); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer %s: %w", GovernanceFinalizer, err)
	}

	return ctrl.Result{}, nil
}

// reconcileCreate handles the logic for a newly created MSR that has not been initialized.
func (r *ManifestSigningRequestReconciler) reconcileCreate(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest, req ctrl.Request) (ctrl.Result, error) { // TODO: be transactional
	r.logger.Info("Initializing new ManifestSigningRequest")

	// Check if MSR is being initialized
	if meta.IsStatusConditionTrue(msr.Status.Conditions, governancev1alpha1.Progressing) {
		r.logger.Info("Initialization is already in progress. Waiting")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// Acquire the lock
	r.logger.Info("Setting Progressing=True to begin initialization")
	meta.SetStatusCondition(&msr.Status.Conditions, metav1.Condition{
		Type:    governancev1alpha1.Progressing,
		Status:  metav1.ConditionTrue,
		Reason:  "CreatingInitialState",
		Message: "Creating default history entry and performing initial Git commit",
	})
	if err := r.Status().Update(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to set Progressing condition")
		return ctrl.Result{}, err
	}

	// Check repository connection
	if _, err := r.repositoryWithError(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to connect to repository.")
		// Release lock with reason failed
		meta.SetStatusCondition(&msr.Status.Conditions, metav1.Condition{
			Type:    governancev1alpha1.Progressing,
			Status:  metav1.ConditionFalse,
			Reason:  "InitializationFailed",
			Message: err.Error(),
		})
		// Also mark Available as false
		meta.SetStatusCondition(&msr.Status.Conditions, metav1.Condition{
			Type:    governancev1alpha1.Available,
			Status:  metav1.ConditionFalse,
			Reason:  "InitializationFailed",
			Message: err.Error(),
		})

		freshMSR := &governancev1alpha1.ManifestSigningRequest{}
		_ = r.Get(ctx, req.NamespacedName, freshMSR)
		freshMSR.Status.Conditions = msr.Status.Conditions
		_ = r.Status().Update(ctx, freshMSR)

		return ctrl.Result{RequeueAfter: 30 * time.Second}, fmt.Errorf("init repo for ManifestSigningRequest: %w", err)
	}

	// Initialization logic
	r.logger.Info("Start saving initial MSR in repository")
	if err := r.saveInRepository(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to save initial ManifestSigningRequest in repository")
		// Release lock with reason failed
		meta.SetStatusCondition(&msr.Status.Conditions, metav1.Condition{
			Type:    governancev1alpha1.Progressing,
			Status:  metav1.ConditionFalse,
			Reason:  "InitializationFailed",
			Message: err.Error(),
		})
		// Also mark Available as false
		meta.SetStatusCondition(&msr.Status.Conditions, metav1.Condition{
			Type:    governancev1alpha1.Available,
			Status:  metav1.ConditionFalse,
			Reason:  "InitializationFailed",
			Message: err.Error(),
		})

		freshMSR := &governancev1alpha1.ManifestSigningRequest{}
		_ = r.Get(ctx, req.NamespacedName, freshMSR)
		freshMSR.Status.Conditions = msr.Status.Conditions
		_ = r.Status().Update(ctx, freshMSR)

		return ctrl.Result{}, fmt.Errorf("save initial ManifestSigningRequest in repository: %w", err)
	}
	r.logger.Info("Finish saving initial MSR in repository")

	// Mark MSR as set up. Take new MSR
	r.logger.Info("Start setting finalizer on the MSR")
	latestMSR := &governancev1alpha1.ManifestSigningRequest{}
	if err := r.Get(ctx, req.NamespacedName, latestMSR); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to re-fetch MSR before finalization: %w", err)
	}

	// Add new initial history record
	newRecord := r.createNewMSRHistoryRecordFromSpec(msr)
	latestMSR.Status.RequestHistory = append(latestMSR.Status.RequestHistory, newRecord)
	// Add finalizer
	controllerutil.AddFinalizer(latestMSR, GovernanceFinalizer)

	// Update status conditions to reflect success
	meta.SetStatusCondition(&latestMSR.Status.Conditions, metav1.Condition{
		Type:    governancev1alpha1.Progressing,
		Status:  metav1.ConditionFalse,
		Reason:  "InitializationSuccessful",
		Message: "Initial state successfully committed to Git and cluster",
	})
	meta.SetStatusCondition(&latestMSR.Status.Conditions, metav1.Condition{
		Type:    governancev1alpha1.Available,
		Status:  metav1.ConditionTrue,
		Reason:  "SetupComplete",
		Message: "Governance is active for this template",
	})

	// Update status
	if err := r.Status().Update(ctx, latestMSR); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply finalizer and set final status: %w", err)
	}
	controllerutil.AddFinalizer(latestMSR, GovernanceFinalizer)
	if err := r.Update(ctx, latestMSR); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply finalizer and set final status: %w", err)
	}
	r.logger.Info("Finish setting finalizer on the MSR")

	r.logger.Info("Successfully finalized ManifestSigningRequest")
	return ctrl.Result{}, nil
}

func (r *ManifestSigningRequestReconciler) reconcileNormal(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest, req ctrl.Request) (ctrl.Result, error) {
	// Check if initialization is still marked as progressing, which shouldn't happen here but is a good safeguard.
	if meta.IsStatusConditionTrue(msr.Status.Conditions, governancev1alpha1.Progressing) {
		r.logger.Info("Waiting for ongoing operation to complete.")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
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
		return ctrl.Result{}, r.handleNewMSRChange(ctx, msr)
	}

	// TODO: Signature Observer logic

	return ctrl.Result{}, nil
}

func (r *ManifestSigningRequestReconciler) handleNewMSRChange(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) error {
	r.logger.Info("Handle new MSR spec change")

	r.logger.Info("Start saving new MSR in repository")
	// Save new MSR in repository
	if err := r.saveInRepository(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to save ManifestSigningRequest in repository")
		return fmt.Errorf("save ManifestSigningRequest in repository: %w", err)
	}
	r.logger.Info("Finish saving new MSR in repository")

	r.logger.Info("Start adding new history record")
	// Add new entry to MSR history
	if err := r.saveNewHistoryRecord(ctx, msr); err != nil {
		r.logger.Error(err, "Failed to save history record for new ManifestSigningRequest")
		return fmt.Errorf("save history record for new ManifestSigningRequest")
	}
	r.logger.Info("Finish adding new history record")

	// Notify the Governors
	if err := r.Notifier.NotifyGovernors(ctx, msr); err != nil {
		// Non-critical error. Log it.
		r.logger.Error(err, "Failed to send notifications to governors")
	} else {
		r.logger.Info("Successfully sent notifications to governors")
	}

	r.logger.Info("Finished MSR spec change")
	return nil
}

func (r *ManifestSigningRequestReconciler) saveInRepository(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) error {
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

func (r *ManifestSigningRequestReconciler) createNewMSRHistoryRecordFromSpec(msr *governancev1alpha1.ManifestSigningRequest) governancev1alpha1.ManifestSigningRequestHistoryRecord {
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
