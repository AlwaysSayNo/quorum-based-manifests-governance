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
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	argocdv1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/controller/api/v1alpha1"
)

const (
	DefaultArgoCDNamespace = "argocd"
	DefaultRequeueDelay    = 10 * time.Second
)

// ArgoCD Application GroupVersionKind
var argocdApplicationGVK = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "Application",
}

// ManifestRequestTemplateReconciler reconciles a ManifestRequestTemplate object
type ManifestRequestTemplateReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestrequesttemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestrequesttemplates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestrequesttemplates/finalizers,verbs=update
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestchangeapprovals,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// This function:
// 1. Detects changes in the ArgoCD Application (new commits)
// 2. Pauses ArgoCD synchronization
// 3. Tracks commit hashes to avoid duplicate processing
// 4. Waits for ManifestChangeApproval before resuming sync
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.1/pkg/reconcile
func (r *ManifestRequestTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	logger.Info("Reconciling ManifestRequestTemplate", "name", req.Name, "namespace", req.Namespace)

	// Fetch the ManifestRequestTemplate instance
	mrt := &governancev1alpha1.ManifestRequestTemplate{}
	if err := r.Get(ctx, req.NamespacedName, mrt); err != nil {
		if errors.IsNotFound(err) {
			// Object doesn't exist. Ignore.
			logger.Info("ManifestRequestTemplate resource not found.")
			return ctrl.Result{}, nil
		}

		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get ManifestRequestTemplate")
		return ctrl.Result{}, err
	}

	// Fetch the ArgoCD Application referenced by the MRT
	appNamespace := mrt.Spec.ArgoCDApplication.Namespace
	if appNamespace == "" {
		appNamespace = DefaultArgoCDNamespace
	}

	app := &argocdv1alpha1.Application{}
	appKey := types.NamespacedName{
		Name:      mrt.Spec.ArgoCDApplication.Name,
		Namespace: appNamespace,
	}

	if err := r.Get(ctx, appKey, app); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("ArgoCD Application not found", "name", appKey.Name, "namespace", appKey.Namespace)
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get ArgoCD Application", "name", appKey.Name, "namespace", appKey.Namespace)
		return ctrl.Result{}, err
	}

	// Get the current commit hash from the Application status
	currentCommitHash := r.getCommitHashFromApp(app)
	if currentCommitHash == "" {
		// TODO: Handle case where commit hash is not found
		logger.Info("No commit hash found in Application status yet")
		return ctrl.Result{}, nil
	}

	// Check if we've already seen this commit hash
	if currentCommitHash == mrt.Status.LastObservedCommitHash {
		logger.Info("Already processed this commit hash", "hash", currentCommitHash)

		// Check if we need to resume sync (if MCA exists)
		// TODO: we need to monitor, if MCA was created and then update the last observed hash.
		// Maybe do with Watches instead of Requeueing?
		if r.hasManifestChangeApproval(ctx, mrt, logger) {
			if err := r.resumeArgoApplication(ctx, app, logger); err != nil {
				logger.Error(err, "Failed to resume ArgoCD Application sync")
				return ctrl.Result{}, err
			}
			logger.Info("Resumed ArgoCD Application sync due to ManifestChangeApproval")
		}

		return ctrl.Result{}, nil
	}

	// New commit detected!
	logger.Info("Detected new commit", "oldHash", mrt.Status.LastObservedCommitHash, "newHash", currentCommitHash)

	// Step 1: Pause ArgoCD sync to prevent automatic synchronization
	if err := r.pauseArgoApplication(ctx, app, logger); err != nil {
		logger.Error(err, "Failed to pause ArgoCD Application sync")
		return ctrl.Result{}, err
	}
	logger.Info("Paused ArgoCD Application sync")

	// Step 2: Update MRT status with the new commit hash
	mrt.Status.LastObservedCommitHash = currentCommitHash
	if err := r.Status().Update(ctx, mrt); err != nil {
		logger.Error(err, "Failed to update ManifestRequestTemplate status")
		return ctrl.Result{}, err
	}
	logger.Info("Updated MRT status with new commit hash", "hash", currentCommitHash)

	// Step 3: Create MSR (Manifest Signing Request) - TODO: implement your MSR creation logic here
	logger.Info("TODO: Create Manifest Signing Request for new commit")

	// Step 4: Requeue after a delay to check for ManifestChangeApproval
	// TODO: we don't need to check for MCA. We can just wait indefinitely until MCA is created and update last observed hash then.
	// Maybe do with Watches instead of Requeueing?
	logger.Info("Requeuing to check for ManifestChangeApproval", "requeueAfter", "10s")
	return ctrl.Result{RequeueAfter: DefaultRequeueDelay}, nil
}

// SetupWithManager sets up the controller with the Manager.
// This controller watches both ManifestRequestTemplate resources and ArgoCD Application resources.
// When an Application changes, it triggers reconciliation of the associated MRT.
func (r *ManifestRequestTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&governancev1alpha1.ManifestRequestTemplate{}).
		Watches(
			&argocdv1alpha1.Application{},
			handler.EnqueueRequestsFromMapFunc(r.findMRTForApplication),
		).
		Named("manifestrequesttemplate").
		Complete(r)
}

// findMRTForApplication maps an ArgoCD Application to the MRT that references it.
// When an Application changes, this function determines which MRT should be reconciled.
func (r *ManifestRequestTemplateReconciler) findMRTForApplication(ctx context.Context, obj client.Object) []reconcile.Request {
	logger := log.FromContext(ctx)

	// Find all MRTs in all namespaces
	mrtList := &governancev1alpha1.ManifestRequestTemplateList{}
	if err := r.List(ctx, mrtList); err != nil {
		logger.Error(err, "Failed to list ManifestRequestTemplates")
		return []reconcile.Request{}
	}

	var requests []reconcile.Request
	for _, mrt := range mrtList.Items {
		// Check if this MRT references the changed Application
		if mrt.Spec.ArgoCDApplication.Name == obj.GetName() {
			appNamespace := mrt.Spec.ArgoCDApplication.Namespace
			if appNamespace == "" {
				appNamespace = DefaultArgoCDNamespace
			}
			if appNamespace == obj.GetNamespace() {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      mrt.Name,
						Namespace: mrt.Namespace,
					},
				})
			}
		}
	}

	return requests
}

// getCommitHashFromApp extracts the current commit hash from the ArgoCD Application status.
// ArgoCD Application stores the current commit hash in status.operationState or status.sync.revision
func (r *ManifestRequestTemplateReconciler) getCommitHashFromApp(app *argocdv1alpha1.Application) string {
	if app == nil {
		return ""
	}

	// Try to get the commit SHA from status.operationState.syncResult.revision
	if app.Status.OperationState != nil && app.Status.OperationState.SyncResult != nil {
		return app.Status.OperationState.SyncResult.Revision
	}

	// Fallback: try status.sync.revision
	if app.Status.Sync.Revision != "" {
		return app.Status.Sync.Revision
	}

	// TODO: what does this mean?
	// Fallback: try status.operationState.finishedAt + resources hash as a fallback unique identifier
	return ""
}

// pauseArgoApplication pauses the automatic sync of an ArgoCD Application.
// This prevents ArgoCD from automatically syncing new commits to the cluster.
func (r *ManifestRequestTemplateReconciler) pauseArgoApplication(ctx context.Context, app *argocdv1alpha1.Application, logger logr.Logger) error {
	// Check if already paused
	if app.Spec.SyncPolicy != nil && app.Spec.SyncPolicy.Automated != nil {
		if !app.Spec.SyncPolicy.Automated.Prune {
			// Already paused
			return nil
		}
	}

	// Set SyncPolicy to pause automated sync
	if app.Spec.SyncPolicy == nil {
		app.Spec.SyncPolicy = &argocdv1alpha1.SyncPolicy{}
	}

	app.Spec.SyncPolicy.Automated = &argocdv1alpha1.SyncPolicyAutomated{
		Prune:      false,
		SelfHeal:   false,
		AllowEmpty: false,
	}

	if err := r.Update(ctx, app); err != nil {
		logger.Error(err, "Failed to update Application with pause sync policy")
		return err
	}

	return nil
}

// resumeArgoApplication resumes the automatic sync of an ArgoCD Application.
// This allows ArgoCD to sync the approved changes to the cluster.
func (r *ManifestRequestTemplateReconciler) resumeArgoApplication(ctx context.Context, app *argocdv1alpha1.Application, logger logr.Logger) error {
	// Check if already resumed
	if app.Spec.SyncPolicy != nil && app.Spec.SyncPolicy.Automated != nil {
		if app.Spec.SyncPolicy.Automated.Prune {
			// Already resumed
			return nil
		}
	}

	// Set SyncPolicy to enable automated sync
	if app.Spec.SyncPolicy == nil {
		app.Spec.SyncPolicy = &argocdv1alpha1.SyncPolicy{}
	}

	app.Spec.SyncPolicy.Automated = &argocdv1alpha1.SyncPolicyAutomated{
		Prune:      true,
		SelfHeal:   true,
		AllowEmpty: false,
	}

	if err := r.Update(ctx, app); err != nil {
		logger.Error(err, "Failed to update Application with resume sync policy")
		return err
	}

	return nil
}

// hasManifestChangeApproval checks if a ManifestChangeApproval (MCA) exists for the MRT.
// This indicates that the changes have been approved and can be synced.
func (r *ManifestRequestTemplateReconciler) hasManifestChangeApproval(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, logger logr.Logger) bool {
	// TODO: Implement this function based on your ManifestChangeApproval resource
	// For now, this is a placeholder that returns false
	// You should:
	// 1. Define the ManifestChangeApproval CRD
	// 2. Query for MCA resources related to this MRT
	// 3. Return true if any approved MCA exists for the current LastObservedCommitHash
	//
	// Example implementation:
	// mcaList := &governancev1alpha1.ManifestChangeApprovalList{}
	// if err := r.List(ctx, mcaList, client.InNamespace(mrt.Namespace)); err != nil {
	//     logger.Error(err, "Failed to list ManifestChangeApprovals")
	//     return false
	// }
	//
	// for _, mca := range mcaList.Items {
	//     if mca.Spec.MRTRef.Name == mrt.Name && mca.Status.Approved {
	//         return true
	//     }
	// }

	logger.Info("TODO: Implement ManifestChangeApproval check")
	return false
}
