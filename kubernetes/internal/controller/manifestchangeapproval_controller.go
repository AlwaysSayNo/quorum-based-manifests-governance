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
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ManifestChangeApprovalReconciler reconciles a ManifestChangeApproval object
type ManifestChangeApprovalReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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
	logger := logf.FromContext(ctx).WithValues("name", req.Name, "namespace", req.Namespace)
	logger.Info("Reconciling ManifestChangeApproval")

	// Fetch the MCA instance
	mca := &governancev1alpha1.ManifestChangeApproval{}
	if err := r.Get(ctx, req.NamespacedName, mca); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetch ManifestChangeApproval for reconcile request: %w", err)
	}

	// Get the latest history record version
	latestHistoryVersion := -1
	history := mca.Status.ApprovalHistory
	if len(history) > 0 {
		latestHistoryVersion = history[len(history)-1].Version
	}

	// If the spec's version is newer - reconcile status
	if mca.Spec.Version > latestHistoryVersion {
		logger.Info("Detected new version in spec, updating status history", "specVersion", mca.Spec.Version, "statusVersion", latestHistoryVersion)
		if resp, err := r.updateStatusOnSpecUpdate(ctx, mca, &logger); err != nil {
			logger.Error(err, "Failed on updating ManifestChangeApproval Status based on Spec")
			return resp, fmt.Errorf("update status on spec update: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

func (r *ManifestChangeApprovalReconciler) updateStatusOnSpecUpdate(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval, logger *logr.Logger) (ctrl.Result, error) {
	// Append a new history record
	newRecord := r.createNewMCAHistoryRecordFromSpec(mca)
	mca.Status.ApprovalHistory = append(mca.Status.ApprovalHistory, newRecord)

	// Save the change
	if err := r.Status().Update(ctx, mca); err != nil {
		logger.Error(err, "Failed to update MCA status with new history record")
		return ctrl.Result{}, fmt.Errorf("update ManifestChangeApproval status with new history record: %w", err)
	}

	logger.Info("Successfully updated MCA status")
	return ctrl.Result{}, nil
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
