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
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
)

// ManifestSigningRequestReconciler reconciles a ManifestSigningRequest object
type ManifestSigningRequestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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
	logger := logf.FromContext(ctx).WithValues("name", req.Name, "namespace", req.Namespace)
	logger.Info("Reconciling ManifestSigningRequest")

	// Fetch the MSR instance
	msr := &governancev1alpha1.ManifestSigningRequest{}
	if err := r.Get(ctx, req.NamespacedName, msr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetch ManifestSigningRequest for reconcile request: %w", err)
	}

	// Get the latest history record version
	latestHistoryVersion := -1
	history := msr.Status.RequestHistory
	if len(history) > 0 {
		latestHistoryVersion = history[len(history)-1].Version
	}

	// If the spec's version is newer - reconcile status
	if msr.Spec.Version > latestHistoryVersion {
		logger.Info("Detected new version in spec, updating status history", "specVersion", msr.Spec.Version, "statusVersion", latestHistoryVersion)
		if resp, err := r.updateStatusOnSpecUpdate(ctx, msr, &logger); err != nil {
			logger.Error(err, "Failed on updating ManifestSigningRequest Status based on Spec")
			return resp, fmt.Errorf("update status on spec update: %w", err)
		}
	}

	// TODO: Signature Observer logic

	return ctrl.Result{}, nil
}

func (r *ManifestSigningRequestReconciler) updateStatusOnSpecUpdate(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest, logger *logr.Logger) (ctrl.Result, error) {
	// Append a new history record
	newRecord := r.createNewMSRHistoryRecordFromSpec(&msr.Spec)
	msr.Status.RequestHistory = append(msr.Status.RequestHistory, newRecord)

	// Save the change
	if err := r.Status().Update(ctx, msr); err != nil {
		logger.Error(err, "Failed to update MSR status with new history record")
		return ctrl.Result{}, fmt.Errorf("update ManifestSigningRequest status with new history record: %w", err)
	}

	logger.Info("Successfully updated MSR status")
	return ctrl.Result{}, nil
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
