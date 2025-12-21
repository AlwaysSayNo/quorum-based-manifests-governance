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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	argocdv1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	repomanager "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	MRTFinalizer      = "governance.nazar.grynko.com/finalizer"
	MRTFinalizerValue = "setup-finished"
)

var (
	logger logr.Logger
)

type RepositoryManager interface {
	GetProviderForMRT(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (repomanager.GitRepository, error)
}

type Notifier interface {
	NotifyGovernors(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, msr *governancev1alpha1.ManifestSigningRequest) error
}

// ManifestRequestTemplateReconciler reconciles a ManifestRequestTemplate object
type ManifestRequestTemplateReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	RepoManager RepositoryManager
	Notifier    Notifier
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
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestchangeapprovals,verbs=get;list;watch
func (r *ManifestRequestTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger = log.FromContext(ctx).WithValues("name", req.Name, "namespace", req.Namespace)

	logger.Info("Reconciling ManifestRequestTemplate")

	// Fetch the MRT instance
	mrt, resp, err := r.getMRTForRequest(ctx, req)
	if err != nil {
		return resp, nil
	}

	// Finalize object, if it's being deleted
	if r.isToFinzalize(ctx, mrt) {
		return r.finzalize(ctx, mrt)
	}

	// Create linked default resources, if it's a new object
	if r.isNewMRTReconcile(mrt) {
		return r.onMRTCreation(ctx, mrt, req)
	}

	// Check, if all linked default resources exist in the cluster
	if err := r.checkDependencies(ctx, mrt); err != nil {
		logger.Error(err, "Failed on dependency check")
		return ctrl.Result{}, fmt.Errorf("check dependencies: %w", err)
	}

	if _, err := r.repositoryWithError(ctx, mrt); err != nil {
		logger.Error(err, "Failed on first repository fetch")
		return ctrl.Result{}, fmt.Errorf("init repo for ManifestRequestTemplate: %w", err)
	}

	// Check, if there is any revision in the queue for review
	if len(mrt.Status.RevisionsQueue) > 0 {
		return r.handleNewRevisionCommit(ctx, req, mrt)
	}

	return ctrl.Result{}, nil
}

// Return true, if MRT is being deleted.
func (r *ManifestRequestTemplateReconciler) isToFinzalize(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) bool {
	return !mrt.ObjectMeta.DeletionTimestamp.IsZero()
}

// isNewMRTReconcile looks for `initial` finalize annotation. If the finalizer isn't set yet, then it's a new MRT.
func (r *ManifestRequestTemplateReconciler) isNewMRTReconcile(mrt *governancev1alpha1.ManifestRequestTemplate) bool {
	return !controllerutil.ContainsFinalizer(mrt, MRTFinalizer)
}

// finalize is used for object clean-up on deletion event.
// So far, it's needed to remove the `initial` finalize annotation.
func (r *ManifestRequestTemplateReconciler) finzalize(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(mrt, MRTFinalizer) {
		// No custom finalizer is found. Do nothing
		return ctrl.Result{}, nil
	}

	// No real clean-up logic is needed
	logger.Info("Successfully finalized ManifestRequestTemplate")

	// Remove the custom finalizer. The object will be deleted
	controllerutil.RemoveFinalizer(mrt, MRTFinalizer)
	if err := r.Update(ctx, mrt); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer %s: %w", MRTFinalizer, err)
	}

	return ctrl.Result{}, nil
}

func (r ManifestRequestTemplateReconciler) onMRTCreation(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, req ctrl.Request) (ctrl.Result, error) {
	logger.Info("Initializing new ManifestRequestTemplate")

	if err := r.createLinkedDefaultResources(ctx, mrt, req); err != nil {
		// If setup fails, we return the error to retry. We don't add the finalizer yet
		return ctrl.Result{}, fmt.Errorf("create default linked resources: %w", err)
	}

	// Mark MRT as set up
	mrt, _, err := r.getMRTForRequest(ctx, req)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("while fetching ManifestRequestTemplate after creating default resources: %w", err)
	}
	controllerutil.AddFinalizer(mrt, MRTFinalizer)
	if err := r.Update(ctx, mrt); err != nil {
		return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
	}

	logger.Info("Successfully finalized ManifestRequestTemplate")

	return ctrl.Result{}, nil
}

// createLinkedDefaultResources performs one-time setup to create linked default resources associated with this MRT.
func (r *ManifestRequestTemplateReconciler) createLinkedDefaultResources(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, req ctrl.Request) error {
	logger.Info("Creating dependent MSR and MCA resources")

	application, _, err := r.getApplication(ctx, mrt)
	if err != nil {
		return fmt.Errorf("fetch Application associated with ManifestRequestTemplate: %w", err)
	}

	mrtMetaRef := governancev1alpha1.VersionedManifestRef{
		Name:      mrt.ObjectMeta.Name,
		Namespace: mrt.ObjectMeta.Namespace,
		Version:   mrt.Spec.Version,
	}

	// Fetch the latest revision from the repository
	revision, err := r.repository(ctx, mrt).GetLatestRevision(ctx)
	if err != nil {
		return fmt.Errorf("fetch latest commit from the repository: %w", err)
	}

	// Fetch all changed files in the repository, that where created before governance process
	fileChanges, err := r.repository(ctx, mrt).GetChangedFiles(ctx, "", revision, application.Spec.Source.Path)
	if err != nil {
		return fmt.Errorf("fetch changes between init commit and %s: %w", revision, err)
	}

	// Create default MSR
	msr := &governancev1alpha1.ManifestSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mrt.Spec.MSR.Name,
			Namespace: mrt.Spec.MSR.Namespace,
		},
		Spec: governancev1alpha1.ManifestSigningRequestSpec{
			Version:       0,
			MRT:           *mrtMetaRef.DeepCopy(),
			PublicKey:     mrt.Spec.PGP.PublicKey,
			GitRepository: governancev1alpha1.GitRepository{URL: application.Spec.Source.RepoURL},
			Location:      *mrt.Spec.Location.DeepCopy(),
			Changes:       fileChanges,
			Governors:     *mrt.Spec.Governors.DeepCopy(),
			Require:       *mrt.Spec.Require.DeepCopy(),
			Status:        governancev1alpha1.Approved,
		},
	}
	// Set MRT as MSR owner
	if err := ctrl.SetControllerReference(mrt, msr, r.Scheme); err != nil {
		logger.Error(err, "Failed to set owner reference on MSR")
		return fmt.Errorf("while setting controllerReference for ManifestSigningRequest: %w", err)
	}
	if err := r.Create(ctx, msr); err != nil {
		if !errors.IsAlreadyExists(err) {
			logger.Error(err, "Failed to create initial MSR")
			return fmt.Errorf("while creating default ManifestSigningRequest: %w", err)
		}
	}

	msrMetaRef := governancev1alpha1.VersionedManifestRef{
		Name:      msr.ObjectMeta.Name,
		Namespace: msr.ObjectMeta.Namespace,
		Version:   msr.Spec.Version,
	}

	// Create default MCA
	mca := &governancev1alpha1.ManifestChangeApproval{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mrt.Spec.MCA.Name,
			Namespace: mrt.Spec.MCA.Namespace,
		},
		Spec: governancev1alpha1.ManifestChangeApprovalSpec{
			Version:               0,
			MRT:                   *mrtMetaRef.DeepCopy(),
			MSR:                   *msrMetaRef.DeepCopy(),
			PublicKey:             mrt.Spec.PGP.PublicKey,
			GitRepository:         governancev1alpha1.GitRepository{URL: application.Spec.Source.RepoURL},
			LastApprovedCommitSHA: revision, // revision, on which MRT should have been created
			Location:              *mrt.Spec.Location.DeepCopy(),
			Changes:               fileChanges,
			Governors:             *mrt.Spec.Governors.DeepCopy(),
			Require:               *mrt.Spec.Require.DeepCopy(),
		},
	}
	// Set MRT as MCA owner
	if err := ctrl.SetControllerReference(mrt, mca, r.Scheme); err != nil {
		logger.Error(err, "Failed to set owner reference on MCA")
		return fmt.Errorf("while setting controllerReference for ManifestChangeApproval: %w", err)
	}
	if err := r.Create(ctx, mca); err != nil {
		if !errors.IsAlreadyExists(err) {
			logger.Error(err, "Failed to create initial MCA")
			return fmt.Errorf("while creating default ManifestChangeApproval: %w", err)
		}
	}

	repositoryMSR := r.createRepositoryMSR(msr)
	repositoryMCA := r.createRepositoryMCA(mca)
	commitHash, err := r.repository(ctx, mrt).PushMSRAndMCA(ctx, &repositoryMSR, &repositoryMCA)

	// Update MRT
	mrt, _, err = r.getMRTForRequest(ctx, req)
	if err != nil {
		return fmt.Errorf("while fetching ManifestRequestTemplate after save: %w", err)
	}
	mrt.Status.LastObservedCommitHash = commitHash
	if err := r.Status().Update(ctx, mrt); err != nil {
		logger.Error(err, "Failed to update initial MRT status")
		return fmt.Errorf("while updating initial ManifestRequestTemplate status: %w", err)
	}

	return nil
}

// checkDependencies validates that all linked resources for an MRT exist.
func (r *ManifestRequestTemplateReconciler) checkDependencies(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) error {
	// Check for Application
	app := &argocdv1alpha1.Application{}
	err := r.Get(ctx, types.NamespacedName{Name: mrt.Spec.ArgoCDApplication.Name, Namespace: mrt.Spec.ArgoCDApplication.Namespace}, app)
	if err != nil {
		logger.Error(err, "Failed to find linked Application")
		return fmt.Errorf("couldn't find linked Application")
	}

	// Check for MSR
	msr := &governancev1alpha1.ManifestSigningRequest{}
	err = r.Get(ctx, types.NamespacedName{Name: mrt.Spec.MSR.Name, Namespace: mrt.Spec.MSR.Namespace}, msr)
	if err != nil {
		logger.Error(err, "Failed to find linked MSR")
		return fmt.Errorf("couldn't find linked ManifestSigningRequest: %w", err)
	}

	// Check for MCA
	mca := &governancev1alpha1.ManifestChangeApproval{}
	err = r.Get(ctx, types.NamespacedName{Name: mrt.Spec.MCA.Name, Namespace: mrt.Spec.MCA.Namespace}, mca)
	if err != nil {
		logger.Error(err, "Failed to find linked MCA")
		return fmt.Errorf("couldn't find linked ManifestChangeApproval: %w", err)
	}

	return nil
}

func (r *ManifestRequestTemplateReconciler) handleNewRevisionCommit(ctx context.Context, req ctrl.Request, mrt *governancev1alpha1.ManifestRequestTemplate) (ctrl.Result, error) {
	if len(mrt.Status.RevisionsQueue) == 0 {
		return ctrl.Result{}, fmt.Errorf("new revision handle failed, since revision queue is empty")
	}

	// Shouldn't be possible, that MCA gets deleted.
	mca, err := r.getMCA(ctx, mrt)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("get ManifestChangeRequest for handling new revision: %w", err)
	}

	revision := mrt.Status.RevisionsQueue[0]
	mcaRevisionIdx := slices.IndexFunc(mca.Status.ApprovalHistory, func(rec governancev1alpha1.ManifestChangeApprovalHistoryRecord) bool {
		return rec.CommitSHA == revision
	})
	if mcaRevisionIdx != -1 && mcaRevisionIdx == len(mca.Status.ApprovalHistory)-1 {
		logger.Info("Revision corresponds to the latest MCA. Do nothing", "revision", revision)
		return r.popFromRevisionQueueWithResult(ctx, mrt)
	} else if mcaRevisionIdx != -1 {
		logger.Info("Revision corresponds to a non latest MCA from History. Might be rollback. No support yet. Do nothing", "revision", revision) // TODO: rollback case
		return r.popFromRevisionQueueWithResult(ctx, mrt)
	}

	if revision == mrt.Status.LastObservedCommitHash {
		logger.Info("Revision corresponds to the latest processed revision. Do nothing", "revision", revision)
		return r.popFromRevisionQueueWithResult(ctx, mrt)
	}

	if hasRevision, err := r.repository(ctx, mrt).HasRevision(ctx, revision); err != nil {
		logger.Error(err, "Failed to check if repository has revision", "revision", revision)
		return ctrl.Result{}, err
	} else if !hasRevision {
		return ctrl.Result{}, fmt.Errorf("no commit for revision %s in the repository", revision)
	}

	latestRevision, err := r.repository(ctx, mrt).GetLatestRevision(ctx)
	if err != nil {
		logger.Error(err, "Failed to fetch last revision from repository", "revision", revision)
	}
	if latestRevision != revision {
		if latestRevision != mrt.Status.LastObservedCommitHash {
			logger.Info("Detected newer latest revision in repository", "revision", revision, "latestRevision", latestRevision)

			// If latest revision not in the queue yet - add to queue
			latestRevisionIdx := slices.IndexFunc(mca.Status.ApprovalHistory, func(rec governancev1alpha1.ManifestChangeApprovalHistoryRecord) bool {
				return rec.CommitSHA == latestRevision
			})
			if latestRevisionIdx == -1 {
				mrt.Status.RevisionsQueue = append(mrt.Status.RevisionsQueue, latestRevision)
			} else {
				return ctrl.Result{}, fmt.Errorf("latest revision not tracked, but has ManifestChangeApproval idx: %d", latestRevisionIdx)
			}
			return r.popFromRevisionQueueWithResult(ctx, mrt)
		}

		logger.Info("Revision corresponds to some old revision from repository. Do nothing", "revision", revision)
		return r.popFromRevisionQueueWithResult(ctx, mrt)
	}

	// MSR process start
	logger.Info("Revision is the latest unprocessed repository revision", "revision", revision)
	return r.startMSRProcess(ctx, req, mrt, mca)
}

func (r *ManifestRequestTemplateReconciler) getMRTForRequest(ctx context.Context, req ctrl.Request) (*governancev1alpha1.ManifestRequestTemplate, ctrl.Result, error) {
	// Fetch the ManifestRequestTemplate instance
	mrt := &governancev1alpha1.ManifestRequestTemplate{}
	if err := r.Get(ctx, req.NamespacedName, mrt); err != nil {
		if errors.IsNotFound(err) {
			// Object doesn't exist. Ignore.
			return nil, ctrl.Result{}, fmt.Errorf("ManifestRequestTemplate resource not found")
		}

		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get ManifestRequestTemplate")
		return nil, ctrl.Result{}, err
	}

	return mrt, ctrl.Result{}, nil
}

func (r *ManifestRequestTemplateReconciler) startMSRProcess(ctx context.Context, req ctrl.Request, mrt *governancev1alpha1.ManifestRequestTemplate, mca *governancev1alpha1.ManifestChangeApproval) (ctrl.Result, error) {
	revision := mrt.Status.RevisionsQueue[0]
	logger.Info("Start MSR process", "revision", revision)

	application, resp, err := r.getApplication(ctx, mrt)
	if err != nil {
		return resp, fmt.Errorf("fetch Application associated with ManifestRequestTemplate: %w", err)
	}

	// Get Changed Files from Git
	changedFiles, err := r.repository(ctx, mrt).GetChangedFiles(ctx, mca.Spec.LastApprovedCommitSHA, revision, application.Spec.Source.Path)
	if err != nil {
		logger.Error(err, "Failed to get changed files from repository")
		// This is a temporary error (e.g., network issue), so we should requeue.
		return ctrl.Result{}, err
	}

	// Filter all files, that ArgoCD doesn't accept/monitor + content of governanceFolder
	changedFiles = r.filterNonManifestFiles(changedFiles, mrt)
	if len(changedFiles) == 0 {
		mrt.Status.LastObservedCommitHash = revision
		logger.Info("No manifest file changes detected between commits. Skipping MSR creation.")
		return r.popFromRevisionQueueWithResult(ctx, mrt)
	}

	// Get and update MSR in cluster
	msr, err := r.getMSR(ctx, mrt)
	if err != nil {
		logger.Error(err, "Failed to fetch MSR by MRT")
		return ctrl.Result{}, err
	}

	updatedMSR := msr.DeepCopy()
	if resp, err = r.updateMSR(ctx, mrt, updatedMSR, revision, changedFiles); err != nil {
		logger.Error(err, "Failed to construct new MSR object")
		return resp, err
	}

	// Create MSR file and push to the Git Repository
	repositoryMSR := r.createRepositoryMSR(updatedMSR)
	msrCommit, err := r.repository(ctx, mrt).PushMSR(ctx, &repositoryMSR)
	if err != nil {
		logger.Error(err, "Failed to push MSR manifest to repository")
		return ctrl.Result{}, err
	}
	logger.Info("Successfully pushed MSR manifest to repository")

	// Update MSR spec in cluster
	if err := r.Update(ctx, updatedMSR); err != nil {
		logger.Error(err, "Failed to update MSR spec in cluster after successful git push")
		return ctrl.Result{}, fmt.Errorf("after successful MSR git push: %w", err)
	}

	// Point to the new MSR commit and pop revision from the queue
	mrt.Status.LastObservedCommitHash = msrCommit
	mrt.Status.RevisionsQueue = mrt.Status.RevisionsQueue[1:]
	if err := r.Status().Update(ctx, mrt); err != nil {
		logger.Error(err, "Failed to update MRT status after MSR creation")
		return ctrl.Result{}, err
	}
	logger.Info("Successfully updated MRT status")

	// Notify the Governors
	if err := r.Notifier.NotifyGovernors(ctx, mrt, msr); err != nil {
		// Non-critical error. Log it.
		logger.Error(err, "Failed to send notifications to governors")
	} else {
		logger.Info("Successfully sent notifications to governors")
	}

	logger.Info("Finished MSR process successfully")
	return ctrl.Result{}, nil
}

func (r *ManifestRequestTemplateReconciler) updateMSR(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, msr *governancev1alpha1.ManifestSigningRequest, revision string, changedFiles []governancev1alpha1.FileChange) (ctrl.Result, error) {
	application, resp, err := r.getApplication(ctx, mrt)
	if err != nil {
		return resp, fmt.Errorf("fetch Application associated with ManifestRequestTemplate: %w", err)
	}

	mrtSpecCpy := mrt.Spec.DeepCopy()

	msr.Spec.Version = msr.Spec.Version + 1
	msr.Spec.MRT = governancev1alpha1.VersionedManifestRef{
		Name:      mrt.ObjectMeta.Name,
		Namespace: mrt.ObjectMeta.Namespace,
		Version:   mrt.Spec.Version,
	}
	msr.Spec.PublicKey = mrtSpecCpy.PGP.PublicKey
	msr.Spec.GitRepository = governancev1alpha1.GitRepository{
		URL: application.Spec.Source.RepoURL,
	}
	msr.Spec.Location = *mrtSpecCpy.Location.DeepCopy()
	msr.Spec.Changes = changedFiles
	msr.Spec.Governors = *mrtSpecCpy.Governors.DeepCopy()
	msr.Spec.Require = *mrtSpecCpy.Require.DeepCopy()
	msr.Spec.Status = governancev1alpha1.InProgress

	return ctrl.Result{}, nil
}

func (r *ManifestRequestTemplateReconciler) createRepositoryMSR(msr *governancev1alpha1.ManifestSigningRequest) governancev1alpha1.ManifestSigningRequestManifestObject {
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

func (r *ManifestRequestTemplateReconciler) createRepositoryMCA(mca *governancev1alpha1.ManifestChangeApproval) governancev1alpha1.ManifestChangeApprovalManifestObject {
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

func (r *ManifestRequestTemplateReconciler) popFromRevisionQueueWithResult(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (ctrl.Result, error) {
	if len(mrt.Status.RevisionsQueue) == 0 {
		return ctrl.Result{}, nil
	}

	mrt.Status.RevisionsQueue = mrt.Status.RevisionsQueue[1:]
	if err := r.Status().Update(ctx, mrt); err != nil {
		logger.Error(err, "Failed to update ManifestRequestTemplate status")
		return ctrl.Result{}, fmt.Errorf("update ManifestRequestTemplate status: %w", err)
	}

	return ctrl.Result{}, nil
}

// getApplication fetches the ArgoCD Application resource referenced by the ManifestRequestTemplate
func (r *ManifestRequestTemplateReconciler) getApplication(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (*argocdv1alpha1.Application, ctrl.Result, error) {
	app := &argocdv1alpha1.Application{}
	appKey := types.NamespacedName{
		Name:      mrt.Spec.ArgoCDApplication.Name,
		Namespace: mrt.Spec.ArgoCDApplication.Namespace,
	}

	if err := r.Get(ctx, appKey, app); err != nil {
		logger.Error(err, "Failed to get ArgoCD Application", "applicationNamespacedName", appKey)
		return nil, ctrl.Result{}, fmt.Errorf("fetch Application associated with ManifestRequestTemplate: %w", err)
	}

	return app, ctrl.Result{}, nil
}

func (r *ManifestRequestTemplateReconciler) getMSR(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (*governancev1alpha1.ManifestSigningRequest, error) {
	msr := &governancev1alpha1.ManifestSigningRequest{}
	msrKey := types.NamespacedName{
		Name:      mrt.Spec.MSR.Name,
		Namespace: mrt.Spec.MSR.Namespace,
	}

	if err := r.Get(ctx, msrKey, msr); err != nil {
		logger.Error(err, "Failed to get ManifestSigningRequest", "manifestSigningRequestNamespacedName", msrKey)
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
		logger.Error(err, "Failed to get ManifestChangeApproval", "manifestChangeApprovalNamespacedName", mcaKey)
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
