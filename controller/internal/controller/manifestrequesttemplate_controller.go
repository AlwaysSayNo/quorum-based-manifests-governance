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

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	argocdv1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/controller/api/v1alpha1"
)

const (
	DefaultArgoCDNamespace = "argocd"
)

type GitRepository interface {
	// HasRevision return true, if revision commit is the part of git repository.
	HasRevision(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, commit string) (bool, error)

	// GetLatestRevision return the last observed revision for the repository.
	GetLatestRevision(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (string, error)

	// GetChangedFiles returns a list of files that changed between two commits.
	// TODO: change type from FileChange. Because it bounds it straight to the governance module
	GetChangedFiles(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, fromCommit, toCommit string) ([]governancev1alpha1.FileChange, error)

	// PushMSR commits and pushes the generated MSR manifest to the correct folder in the repo.
	PushMSR(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, msr *governancev1alpha1.ManifestSigningRequest) (string, error)
}

type Notifier interface {
	NotifyGovernors(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, msr *governancev1alpha1.ManifestSigningRequest) error
}

// ManifestRequestTemplateReconciler reconciles a ManifestRequestTemplate object
type ManifestRequestTemplateReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	repository GitRepository
	notifier   Notifier
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
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestchangeapprovals,verbs=get;list;watch

func (r *ManifestRequestTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	logger.Info("Reconciling ManifestRequestTemplate", "name", req.Name, "namespace", req.Namespace)

	// Fetch the MRT instance
	mrt, res, err := r.getMRTForRequest(ctx, req, &logger)
	if res != nil {
		return *res, err
	}

	// TODO: remove nil branch, after defaulting webhook
	if len(mrt.Status.RevisionsQueue) > 0 {
		return r.handleNewRevisionCommit(ctx, req, mrt, &logger)
	}

	return ctrl.Result{}, nil
}

func (r *ManifestRequestTemplateReconciler) handleNewRevisionCommit(ctx context.Context, req ctrl.Request, mrt *governancev1alpha1.ManifestRequestTemplate, logger *logr.Logger) (ctrl.Result, error) {
	// TODO: remove nil branch, after defaulting webhook
	if len(mrt.Status.RevisionsQueue) == 0 {
		return ctrl.Result{}, fmt.Errorf("new revision handle failed, since revision queue is empty")
	}

	// TODO: in defaulting webhook we should intercept all MRT template CREATE requests and create corresponding default MCA for it, in order to avoid null checking MCA
	// TODO: validate, if someone tries to delete MCA -- not allowed
	mca, res, err := r.getMCAForMRT(ctx, mrt, logger)
	if err != nil {
		return res, err
	}

	revision := mrt.Status.RevisionsQueue[0]
	mcaRevisionIdx := slices.IndexFunc(mca.Status.ApprovalHistory, func(rec governancev1alpha1.ManifestChangeApprovalHistoryRecord) bool {
		return rec.CommitSHA == revision
	})
	if mcaRevisionIdx == len(mca.Status.ApprovalHistory)-1 {
		logger.Info("Revision %s corresponds to the latest MCA. Do nothing", "revision", revision, "name", req.Name, "namespace", req.Namespace)
		return r.popFromRevisionQueueWithResult(ctx, mrt, logger)
	} else if mcaRevisionIdx != -1 {
		logger.Info("Revision corresponds to a non latest MCA from History. Might be rollback. No support yet. Do nothing", "revision", revision, "name", req.Name, "namespace", req.Namespace)
		return r.popFromRevisionQueueWithResult(ctx, mrt, logger)
	}

	if revision == mrt.Status.LastObservedCommitHash {
		logger.Info("Revision corresponds to the latest processed revision. Do nothing", "revision", revision, "name", req.Name, "namespace", req.Namespace)
		return ctrl.Result{}, nil
	}

	if hasRevision, err := r.repository.HasRevision(ctx, mrt, revision); err != nil {
		logger.Error(err, "Failed to check if repository has revision", "revision", revision, "name", req.Name, "namespace", req.Namespace)
		return ctrl.Result{}, err
	} else if !hasRevision {
		return ctrl.Result{}, fmt.Errorf("no commit for revision %s in the repository", revision)
	}

	latestRevision, err := r.repository.GetLatestRevision(ctx, mrt)
	if err != nil {
		logger.Error(err, "Failed to fetch last revision from repository", "revision", revision, "name", req.Name, "namespace", req.Namespace)
	}
	if latestRevision != revision {
		if latestRevision != mrt.Status.LastObservedCommitHash {
			logger.Info("Detected newer latest revision in repository", "revision", revision, "latestRevision", latestRevision)
			mrt.Status.RevisionsQueue = append(mrt.Status.RevisionsQueue, latestRevision)
			return r.popFromRevisionQueueWithResult(ctx, mrt, logger)
		}

		logger.Info("Revision corresponds to some old revision from repository. Do nothing", "revision", revision, "name", req.Name, "namespace", req.Namespace)
		return r.popFromRevisionQueueWithResult(ctx, mrt, logger)
	}

	// MSR process start
	logger.Info("Revision is the latest unprocessed repository revision", "revision", revision, "name", req.Name, "namespace", req.Namespace)
	return r.startMSRProcess(ctx, req, mrt, mca, logger)
}

func (r *ManifestRequestTemplateReconciler) getMRTForRequest(ctx context.Context, req ctrl.Request, logger *logr.Logger) (*governancev1alpha1.ManifestRequestTemplate, *ctrl.Result, error) {
	// Fetch the ManifestRequestTemplate instance
	mrt := &governancev1alpha1.ManifestRequestTemplate{}
	if err := r.Get(ctx, req.NamespacedName, mrt); err != nil {
		if errors.IsNotFound(err) {
			// Object doesn't exist. Ignore.
			logger.Info("ManifestRequestTemplate resource not found.")
			return nil, &ctrl.Result{}, nil
		}

		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get ManifestRequestTemplate")
		return nil, &ctrl.Result{}, err
	}

	return mrt, nil, nil
}

func (r *ManifestRequestTemplateReconciler) getMCAForMRT(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, logger *logr.Logger) (*governancev1alpha1.ManifestChangeApproval, ctrl.Result, error) {
	// Fetch list of all MRTs
	mcaList := &governancev1alpha1.ManifestChangeApprovalList{}
	if err := r.Client.List(ctx, mcaList); err != nil {
		logger.Error(err, "Failed to get ManifestChangeApproval list while getting MCA")
		return nil, ctrl.Result{}, fmt.Errorf("list ManifestChangeApproval: %w", err)
	}

	// Find MCA for this revision
	for _, mcaItem := range mcaList.Items {
		if mcaItem.Name == mrt.Spec.MCA.Name && mcaItem.Namespace == mrt.Spec.MCA.Namespace {
			return &mcaItem, ctrl.Result{}, nil
		}
	}

	// No MCA found
	return nil, ctrl.Result{}, fmt.Errorf("no MCA for MRT was found. By default, MRT always has at least default MCA")
}

func (r *ManifestRequestTemplateReconciler) getMSRForMRT(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, logger *logr.Logger) (*governancev1alpha1.ManifestSigningRequest, ctrl.Result, error) {
	// Fetch list of all MSRs
	msrList := &governancev1alpha1.ManifestSigningRequestList{}
	if err := r.Client.List(ctx, msrList); err != nil {
		logger.Error(err, "Failed to get ManifestSigningRequest list while getting MCA")
		return nil, ctrl.Result{}, fmt.Errorf("list ManifestSigningRequest: %w", err)
	}

	// Find MSR for this MRT
	for _, msrItem := range msrList.Items {
		if msrItem.Name == mrt.Spec.MCA.Name && msrItem.Namespace == mrt.Spec.MCA.Namespace {
			return &msrItem, ctrl.Result{}, nil
		}
	}

	// No MCA found
	return nil, ctrl.Result{}, fmt.Errorf("no MSR for MRT was found. By default, MRT always has at least default MSR")
}

func (r *ManifestRequestTemplateReconciler) startMSRProcess(ctx context.Context, req ctrl.Request, mrt *governancev1alpha1.ManifestRequestTemplate, mca *governancev1alpha1.ManifestChangeApproval, logger *logr.Logger) (ctrl.Result, error) {
	revision := mrt.Status.RevisionsQueue[0]
	logger.Info("Start MSR process", "revision", revision, "name", req.Name, "namespace", req.Namespace)

	// Get Changed Files from Git
	changedFiles, err := r.repository.GetChangedFiles(ctx, mrt, mca.Status.LastApprovedCommitSHA, revision)
	if err != nil {
		logger.Error(err, "Failed to get changed files from repository")
		// This is a temporary error (e.g., network issue), so we should requeue.
		return ctrl.Result{}, err
	}

	// Filter all files, that ArgoCD don't accept/monitor + content of governanceFolder
	// TODO: take SHA from files
	changedFiles = r.filterNonManifestFiles(changedFiles, mrt)
	if len(changedFiles) == 0 {
		mrt.Status.LastObservedCommitHash = revision
		logger.Info("No manifest file changes detected between commits. Skipping MSR creation.")
		return r.popFromRevisionQueueWithResult(ctx, mrt, logger)
	}

	// Get and update MSR in cluster
	msr, resp, err := r.getMSRForMRT(ctx, mrt, logger)
	if err != nil {
		logger.Error(err, "Failed to fetch MSR by MRT")
		return resp, err
	}

	resp, err = r.updateMSR(ctx, mrt, msr, revision, changedFiles, logger)
	if err != nil {
		logger.Error(err, "Failed to construct new MSR object")
		return resp, err
	}

	// Create MSR file and push to the Git Repository
	msrCommit, err := r.repository.PushMSR(ctx, mrt, msr)
	if err != nil {
		logger.Error(err, "Failed to push MSR manifest to repository")
		return ctrl.Result{}, err
	}
	logger.Info("Successfully pushed MSR manifest to repository")

	// Point to the new MSR commit and pop revision from the queue
	mrt.Status.LastObservedCommitHash = msrCommit
	mrt.Status.RevisionsQueue = mrt.Status.RevisionsQueue[1:]
	if err := r.Status().Update(ctx, mrt); err != nil {
		logger.Error(err, "Failed to update MRT status after MSR creation")
		return ctrl.Result{}, err
	}
	logger.Info("Successfully updated MRT status")

	// Notify the Governors
	if err := r.notifier.NotifyGovernors(ctx, mrt, msr); err != nil {
		// Non-critical error. Log it.
		logger.Error(err, "Failed to send notifications to governors")
	} else {
		logger.Info("Successfully sent notifications to governors")
	}

	logger.Info("Finished MSR process successfully")
	return ctrl.Result{}, nil
}

// TODO: MRT should be set as the owner of MSR and MCA
func (r *ManifestRequestTemplateReconciler) updateMSR(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, msr *governancev1alpha1.ManifestSigningRequest, revision string, changedFiles []governancev1alpha1.FileChange, logger *logr.Logger) (ctrl.Result, error) {
	application, resp, err := r.getApplication(ctx, mrt, logger)
	if err != nil {
		return resp, err
	}

	mrtSpecCpy := mrt.Spec.DeepCopy()

	newVersion := msr.Spec.Version + 1
	newGitRepository := governancev1alpha1.GitRepository{
		URL:  application.Spec.Source.RepoURL,
		Path: application.Spec.Source.Path,
	}

	msr.Spec.Version = newVersion
	msr.Spec.PublicKey = mrtSpecCpy.PublicKey
	msr.Spec.GitRepository = newGitRepository
	msr.Spec.Location = mrtSpecCpy.Location
	msr.Spec.Changes = changedFiles
	msr.Spec.Governors = mrtSpecCpy.Governors
	msr.Spec.Require = mrtSpecCpy.Require

	msr.Status.RequestHistory = append(msr.Status.RequestHistory, r.createNewMSRHistoryRecordFromMSR(msr))

	if err := r.Status().Update(ctx, msr); err != nil {
		logger.Error(err, "Failed to update MSR after new MSR request creation")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ManifestRequestTemplateReconciler) createNewMSRHistoryRecordFromMSR(msr *governancev1alpha1.ManifestSigningRequest) governancev1alpha1.ManifestSigningRequestHistoryRecord {
	msrSpecCpy := msr.Spec.DeepCopy()

	return governancev1alpha1.ManifestSigningRequestHistoryRecord{
		Version:   msrSpecCpy.Version,
		Changes:   msrSpecCpy.Changes,
		Governors: msrSpecCpy.Governors,
		Require:   msrSpecCpy.Require,
		Approves: governancev1alpha1.GovernorList{
			Members: []governancev1alpha1.Governor{},
		},
		Status: governancev1alpha1.InProgress,
	}
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
			strings.HasSuffix(filePath, ".json")

		if !isManifestType {
			continue
		}

		// Skip files inside of governanceFolder
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

func (r *ManifestRequestTemplateReconciler) popFromRevisionQueueWithResult(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, logger *logr.Logger) (ctrl.Result, error) {
	mrt.Status.RevisionsQueue = mrt.Status.RevisionsQueue[:1]
	if err := r.Status().Update(ctx, mrt); err != nil {
		logger.Error(err, "Failed to update ManifestRequestTemplate status")
		return ctrl.Result{}, fmt.Errorf("update ManifestRequestTemplate status: %w", err)
	}

	return ctrl.Result{}, nil
}

// getApplication fetches the ArgoCD Application resource referenced by the ManifestRequestTemplate
func (r *ManifestRequestTemplateReconciler) getApplication(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, logger *logr.Logger) (*argocdv1alpha1.Application, ctrl.Result, error) {
	// TODO: create a defaulting webhook to set these values, in order to avoid validating it every time.
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
		// TODO: create validating webhook, that ensures, that MRT corresponds to an existing Application
		// TODO: In validating webhook we can also set mapping for Application to MRT (in case of non default Application namespace)
		if errors.IsNotFound(err) {
			logger.Info("ArgoCD Application not found", "name", appKey.Name, "namespace", appKey.Namespace)
			return nil, ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get ArgoCD Application", "name", appKey.Name, "namespace", appKey.Namespace)
		return nil, ctrl.Result{}, err
	}

	return app, ctrl.Result{}, nil
}
