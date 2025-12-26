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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	repomanager "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository"
)

const (
	GovernanceFinalizer       = "governance.nazar.grynko.com/finalizer"
	QubmangoOperationalFolder = ".qubmango"
	QubmangoOperationalFile   = QubmangoOperationalFolder + "/index.yaml"
)

type RepositoryManager interface {
	GetProviderForMRT(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (repomanager.GitRepository, error)
}

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
		For(&governancev1alpha1.ManifestRequestTemplate{}).
		Named("manifestrequesttemplate").
		Complete(r)
}

// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestrequesttemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestrequesttemplates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestrequesttemplates/finalizers,verbs=update
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestsigningrequests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=manifestchangeapprovals,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *ManifestRequestTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = log.FromContext(ctx).WithValues("controller", "ManifestRequestTemplate", "name", req.Name, "namespace", req.Namespace)

	r.logger.Info("Reconciling ManifestRequestTemplate")

	// Fetch the MRT instance
	mrt := &governancev1alpha1.ManifestRequestTemplate{}
	if err := r.Get(ctx, req.NamespacedName, mrt); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if !mrt.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, mrt)
	}

	// Handle initialization
	if !controllerutil.ContainsFinalizer(mrt, GovernanceFinalizer) {
		return r.reconcileCreate(ctx, mrt, req)
	}

	// Handle normal reconciliation
	return r.reconcileNormal(ctx, mrt, req)
}

// reconcileDelete handles the cleanup logic when an MRT is being deleted.
func (r *ManifestRequestTemplateReconciler) reconcileDelete(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(mrt, GovernanceFinalizer) {
		// No custom finalizer is found. Do nothing
		return ctrl.Result{}, nil
	}

	// No real clean-up logic is needed
	r.logger.Info("Successfully finalized ManifestRequestTemplate")

	// Remove the custom finalizer. The object will be deleted
	controllerutil.RemoveFinalizer(mrt, GovernanceFinalizer)
	if err := r.Update(ctx, mrt); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer %s: %w", GovernanceFinalizer, err)
	}

	return ctrl.Result{}, nil
}

// reconcileCreate handles the logic for a newly created MRT that has not been initialized.
func (r ManifestRequestTemplateReconciler) reconcileCreate(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, req ctrl.Request) (ctrl.Result, error) {
	r.logger.Info("Initializing new ManifestRequestTemplate")

	// Check if MRT is being initialized
	if meta.IsStatusConditionTrue(mrt.Status.Conditions, governancev1alpha1.Progressing) {
		r.logger.Info("Initialization is already in progress. Waiting")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// Acquire the lock
	r.logger.Info("Setting Progressing=True to begin initialization")
	meta.SetStatusCondition(&mrt.Status.Conditions, metav1.Condition{
		Type:    governancev1alpha1.Progressing,
		Status:  metav1.ConditionTrue,
		Reason:  "CreatingInitialState",
		Message: "Creating default linked resources and performing initial Git commit",
	})
	if err := r.Status().Update(ctx, mrt); err != nil {
		r.logger.Error(err, "Failed to set Progressing condition")
		// Retry if we can't set the lock.
		return ctrl.Result{}, err
	}

	// Initialization logic
	r.logger.Info("Start creating default linked resources")
	initialCommitHash, err := r.createLinkedDefaultResources(ctx, mrt)
	if err != nil {
		r.logger.Error(err, "Failed during initialization.")
		// Release lock with reason failed
		meta.SetStatusCondition(&mrt.Status.Conditions, metav1.Condition{
			Type:    governancev1alpha1.Progressing,
			Status:  metav1.ConditionFalse,
			Reason:  "InitializationFailed",
			Message: err.Error(),
		})
		// Also mark Available as false
		meta.SetStatusCondition(&mrt.Status.Conditions, metav1.Condition{
			Type:    governancev1alpha1.Available,
			Status:  metav1.ConditionFalse,
			Reason:  "InitializationFailed",
			Message: err.Error(),
		})

		freshMRT := &governancev1alpha1.ManifestRequestTemplate{}
		_ = r.Get(ctx, req.NamespacedName, freshMRT)
		freshMRT.Status.Conditions = mrt.Status.Conditions
		_ = r.Status().Update(ctx, freshMRT)

		return ctrl.Result{}, fmt.Errorf("create default linked resources: %w", err)
	}
	r.logger.Info("Finish creating default linked resources")

	// Mark MRT as set up. Take new MRT
	r.logger.Info("Start setting finalizer on the MRT")

	// Add new initial history record and finalizer
	controllerutil.AddFinalizer(mrt, GovernanceFinalizer)
	mrt.Status.LastObservedCommitHash = initialCommitHash

	// Update status conditions to reflect success
	meta.SetStatusCondition(&mrt.Status.Conditions, metav1.Condition{
		Type:    governancev1alpha1.Progressing,
		Status:  metav1.ConditionFalse,
		Reason:  "InitializationSuccessful",
		Message: "Initial state successfully committed to Git and cluster",
	})
	meta.SetStatusCondition(&mrt.Status.Conditions, metav1.Condition{
		Type:    governancev1alpha1.Available,
		Status:  metav1.ConditionTrue,
		Reason:  "SetupComplete",
		Message: "Governance is active for this template",
	})

	// Update status
	if err := r.Status().Update(ctx, mrt); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply finalizer and set final status: %w", err)
	}
	controllerutil.AddFinalizer(mrt, GovernanceFinalizer)
	if err := r.Update(ctx, mrt); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply finalizer and set final status: %w", err)
	}
	r.logger.Info("Finish setting finalizer on the MRT")

	r.logger.Info("Successfully finalized ManifestRequestTemplate")
	return ctrl.Result{}, nil
}

// createLinkedDefaultResources performs one-time setup to create linked default resources associated with this MRT. Return initial commit back
func (r *ManifestRequestTemplateReconciler) createLinkedDefaultResources(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (string, error) {
	r.logger.Info("Creating dependent MSR and MCA resources")

	application, err := r.getApplication(ctx, mrt)
	if err != nil {
		return "", fmt.Errorf("fetch Application associated with ManifestRequestTemplate: %w", err)
	}

	if _, err := r.repositoryWithError(ctx, mrt); err != nil {
		r.logger.Error(err, "Failed on first repository fetch")
		return "", fmt.Errorf("init repo for ManifestRequestTemplate: %w", err)
	}

	// Fetch the latest revision from the repository
	revision, err := r.repository(ctx, mrt).GetLatestRevision(ctx)
	if err != nil {
		return "", fmt.Errorf("fetch latest commit from the repository: %w", err)
	}

	// Fetch all changed files in the repository, that where created before governance process
	fileChanges, err := r.repository(ctx, mrt).GetChangedFiles(ctx, "", revision, application.Spec.Source.Path)
	if err != nil {
		return "", fmt.Errorf("fetch changes between init commit and %s: %w", revision, err)
	}

	// Create default MSR
	msr := r.buildInitialMSR(mrt, fileChanges, revision)
	// Set MRT as MSR owner
	if err := ctrl.SetControllerReference(mrt, msr, r.Scheme); err != nil {
		r.logger.Error(err, "Failed to set owner reference on MSR")
		return "", fmt.Errorf("while setting controllerReference for ManifestSigningRequest: %w", err)
	}
	if err := r.Create(ctx, msr); err != nil {
		if !errors.IsAlreadyExists(err) {
			r.logger.Error(err, "Failed to create initial MSR")
			return "", fmt.Errorf("while creating default ManifestSigningRequest: %w", err)
		}
	}

	// Create default MCA
	mca := r.buildInitialMCA(mrt, msr, fileChanges, revision)
	// Set MRT as MCA owner
	if err := ctrl.SetControllerReference(mrt, mca, r.Scheme); err != nil {
		r.logger.Error(err, "Failed to set owner reference on MCA")
		return "", fmt.Errorf("while setting controllerReference for ManifestChangeApproval: %w", err)
	}
	if err := r.Create(ctx, mca); err != nil {
		if !errors.IsAlreadyExists(err) {
			r.logger.Error(err, "Failed to create initial MCA")
			return "", fmt.Errorf("while creating default ManifestChangeApproval: %w", err)
		}
	}

	// Create entry in the index file
	governanceIndexAlias := mrt.Namespace + ":" + mrt.Name
	commitHash, err := r.repository(ctx, mrt).InitializeGovernance(ctx, QubmangoOperationalFile, governanceIndexAlias, mrt.Spec.Location.Folder)
	if err != nil {
		// TODO: do rollback of the files in the cluster
		return "", fmt.Errorf("save initial ManifestSigningRequest and ManifestChangeApproval to repository: %w", err)
	}

	return commitHash, nil
}

func (r *ManifestRequestTemplateReconciler) buildInitialMSR(
	mrt *governancev1alpha1.ManifestRequestTemplate,
	fileChanges []governancev1alpha1.FileChange,
	revision string,
) *governancev1alpha1.ManifestSigningRequest {

	mrtMetaRef := governancev1alpha1.VersionedManifestRef{
		Name:      mrt.ObjectMeta.Name,
		Namespace: mrt.ObjectMeta.Namespace,
		Version:   mrt.Spec.Version,
	}

	return &governancev1alpha1.ManifestSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mrt.Spec.MSR.Name,
			Namespace: mrt.Spec.MSR.Namespace,
		},
		Spec: governancev1alpha1.ManifestSigningRequestSpec{
			Version:   0,
			MRT:       *mrtMetaRef.DeepCopy(),
			PublicKey: mrt.Spec.PGP.PublicKey,
			GitRepository: governancev1alpha1.GitRepository{
				SSHURL: mrt.Spec.GitRepository.SSHURL,
			},
			Location:  *mrt.Spec.Location.DeepCopy(),
			Changes:   fileChanges,
			Governors: *mrt.Spec.Governors.DeepCopy(),
			Require:   *mrt.Spec.Require.DeepCopy(),
			Status:    governancev1alpha1.Approved,
		},
	}
}

func (r *ManifestRequestTemplateReconciler) buildInitialMCA(
	mrt *governancev1alpha1.ManifestRequestTemplate,
	msr *governancev1alpha1.ManifestSigningRequest,
	fileChanges []governancev1alpha1.FileChange,
	revision string,
) *governancev1alpha1.ManifestChangeApproval {

	mrtMetaRef := governancev1alpha1.VersionedManifestRef{
		Name:      mrt.ObjectMeta.Name,
		Namespace: mrt.ObjectMeta.Namespace,
		Version:   mrt.Spec.Version,
	}

	msrMetaRef := governancev1alpha1.VersionedManifestRef{
		Name:      msr.ObjectMeta.Name,
		Namespace: msr.ObjectMeta.Namespace,
		Version:   msr.Spec.Version,
	}

	return &governancev1alpha1.ManifestChangeApproval{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mrt.Spec.MCA.Name,
			Namespace: mrt.Spec.MCA.Namespace,
		},
		Spec: governancev1alpha1.ManifestChangeApprovalSpec{
			Version:   0,
			MRT:       *mrtMetaRef.DeepCopy(),
			MSR:       *msrMetaRef.DeepCopy(),
			PublicKey: mrt.Spec.PGP.PublicKey,
			GitRepository: governancev1alpha1.GitRepository{
				SSHURL: mrt.Spec.GitRepository.SSHURL,
			},
			LastApprovedCommitSHA: revision, // revision, on which MRT should have been created
			Location:              *mrt.Spec.Location.DeepCopy(),
			Changes:               fileChanges,
			Governors:             *mrt.Spec.Governors.DeepCopy(),
			Require:               *mrt.Spec.Require.DeepCopy(),
		},
	}
}

// reconcileNormal handles the main business logic for an initialized MRT.
func (r *ManifestRequestTemplateReconciler) reconcileNormal(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, req ctrl.Request) (ctrl.Result, error) {
	if meta.IsStatusConditionTrue(mrt.Status.Conditions, governancev1alpha1.Progressing) {
		r.logger.Info("Waiting for ongoing operation to complete.")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// Check dependencies
	if err := r.checkDependencies(ctx, mrt); err != nil {
		r.logger.Error(err, "Failed on dependency check. Requeuing.")
		// Update status to Available=False
		meta.SetStatusCondition(&mrt.Status.Conditions, metav1.Condition{
			Type:    governancev1alpha1.Available,
			Status:  metav1.ConditionFalse,
			Reason:  "DependenciesMissing",
			Message: err.Error(),
		})
		_ = r.Status().Update(ctx, mrt)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Check repository connection
	if _, err := r.repositoryWithError(ctx, mrt); err != nil {
		r.logger.Error(err, "Failed to connect to repository.")
		meta.SetStatusCondition(&mrt.Status.Conditions, metav1.Condition{
			Type:    governancev1alpha1.Available,
			Status:  metav1.ConditionFalse,
			Reason:  "DependenciesMissing",
			Message: err.Error(),
		})
		_ = r.Status().Update(ctx, mrt)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, fmt.Errorf("init repo for ManifestRequestTemplate: %w", err)
	}

	var err error = nil
	if len(mrt.Status.RevisionsQueue) > 0 {
		// handleNewRevisionCommit should return a Result and an Error
		err = r.handleNewRevisionCommit(ctx, req, mrt)
	}

	// All dependencies are met, no new revisions. Mark as Available=True.
	if err == nil && !meta.IsStatusConditionTrue(mrt.Status.Conditions, "Available") {
		meta.SetStatusCondition(&mrt.Status.Conditions, metav1.Condition{
			Type:    governancev1alpha1.Available,
			Status:  metav1.ConditionTrue,
			Reason:  "Ready",
			Message: "All dependencies are met and controller is ready.",
		})
		if err := r.Status().Update(ctx, mrt); err != nil {
			return ctrl.Result{}, fmt.Errorf("update status condition to Available: %w", err)
		}
	}

	return ctrl.Result{}, err
}

// checkDependencies validates that all linked resources for an MRT exist.
func (r *ManifestRequestTemplateReconciler) checkDependencies(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) error {
	// Check for Application
	app := &argocdv1alpha1.Application{}
	err := r.Get(ctx, types.NamespacedName{Name: mrt.Spec.ArgoCDApplication.Name, Namespace: mrt.Spec.ArgoCDApplication.Namespace}, app)
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

	return nil
}

func (r *ManifestRequestTemplateReconciler) handleNewRevisionCommit(ctx context.Context, req ctrl.Request, mrt *governancev1alpha1.ManifestRequestTemplate) error {
	if len(mrt.Status.RevisionsQueue) == 0 {
		return fmt.Errorf("new revision handle failed, since revision queue is empty")
	}

	// Shouldn't be possible, that MCA gets deleted.
	mca, err := r.getMCA(ctx, mrt)
	if err != nil {
		return fmt.Errorf("get ManifestChangeRequest for handling new revision: %w", err)
	}

	revision := mrt.Status.RevisionsQueue[0]
	mcaRevisionIdx := slices.IndexFunc(mca.Status.ApprovalHistory, func(rec governancev1alpha1.ManifestChangeApprovalHistoryRecord) bool {
		return rec.CommitSHA == revision
	})
	if mcaRevisionIdx != -1 && mcaRevisionIdx == len(mca.Status.ApprovalHistory)-1 {
		r.logger.Info("Revision corresponds to the latest MCA. Do nothing", "revision", revision)
		return r.popFromRevisionQueueWithResult(ctx, mrt)
	} else if mcaRevisionIdx != -1 {
		r.logger.Info("Revision corresponds to a non latest MCA from History. Might be rollback. No support yet. Do nothing", "revision", revision) // TODO: rollback case
		return r.popFromRevisionQueueWithResult(ctx, mrt)
	}

	if revision == mrt.Status.LastObservedCommitHash {
		r.logger.Info("Revision corresponds to the latest processed revision. Do nothing", "revision", revision)
		return r.popFromRevisionQueueWithResult(ctx, mrt)
	}

	if hasRevision, err := r.repository(ctx, mrt).HasRevision(ctx, revision); err != nil {
		r.logger.Error(err, "Failed to check if repository has revision", "revision", revision)
		return err
	} else if !hasRevision {
		return fmt.Errorf("no commit for revision %s in the repository", revision)
	}

	latestRevision, err := r.repository(ctx, mrt).GetLatestRevision(ctx)
	if err != nil {
		r.logger.Error(err, "Failed to fetch last revision from repository", "revision", revision)
		return fmt.Errorf("fetch latest revision from repository: %w", err)
	}
	if latestRevision != revision {
		if latestRevision != mrt.Status.LastObservedCommitHash {
			r.logger.Info("Detected newer latest revision in repository", "revision", revision, "latestRevision", latestRevision)

			// If latest revision not in the queue yet - add to queue
			latestRevisionIdx := slices.IndexFunc(mca.Status.ApprovalHistory, func(rec governancev1alpha1.ManifestChangeApprovalHistoryRecord) bool {
				return rec.CommitSHA == latestRevision
			})
			if latestRevisionIdx == -1 {
				mrt.Status.RevisionsQueue = append(mrt.Status.RevisionsQueue, latestRevision)
			} else {
				return fmt.Errorf("latest revision not tracked, but has ManifestChangeApproval idx: %d", latestRevisionIdx)
			}
			return r.popFromRevisionQueueWithResult(ctx, mrt)
		}

		r.logger.Info("Revision corresponds to some old revision from repository. Do nothing", "revision", revision)
		return r.popFromRevisionQueueWithResult(ctx, mrt)
	}

	// MSR process start
	r.logger.Info("Revision is the latest unprocessed repository revision", "revision", revision)
	return r.startMSRProcess(ctx, req, mrt, mca)
}

func (r *ManifestRequestTemplateReconciler) startMSRProcess(ctx context.Context, req ctrl.Request, mrt *governancev1alpha1.ManifestRequestTemplate, mca *governancev1alpha1.ManifestChangeApproval) error {
	revision := mrt.Status.RevisionsQueue[0]
	r.logger.Info("Start MSR process", "revision", revision)

	application, err := r.getApplication(ctx, mrt)
	if err != nil {
		return fmt.Errorf("fetch Application associated with ManifestRequestTemplate: %w", err)
	}

	// Get Changed Files from Git
	changedFiles, err := r.repository(ctx, mrt).GetChangedFiles(ctx, mca.Spec.LastApprovedCommitSHA, revision, application.Spec.Source.Path)
	if err != nil {
		r.logger.Error(err, "Failed to get changed files from repository")
		// This is a temporary error (e.g., network issue), so we should requeue.
		return err
	}

	// Filter all files, that ArgoCD doesn't accept/monitor + content of governanceFolder
	changedFiles = r.filterNonManifestFiles(changedFiles, mrt)
	if len(changedFiles) == 0 {
		mrt.Status.LastObservedCommitHash = revision
		r.logger.Info("No manifest file changes detected between commits. Skipping MSR creation.")
		return r.popFromRevisionQueueWithResult(ctx, mrt)
	}

	// Get and update MSR in cluster
	msr, err := r.getMSR(ctx, mrt)
	if err != nil {
		r.logger.Error(err, "Failed to fetch MSR by MRT")
		return err
	}

	// Created updated version of MSR with higher version
	updatedMSR := r.getMSRWithNewVersion(ctx, mrt, msr, revision, changedFiles)

	// Update MSR spec in cluster. Trigger so push of new MSR to git repository from MSR controller
	if err := r.Update(ctx, updatedMSR); err != nil {
		r.logger.Error(err, "Failed to update MSR spec in cluster after successful revision processed")
		return fmt.Errorf("after successful MSR revision processed: %w", err)
	}

	// Point to the new MSR commit and pop revision from the queue
	mrt.Status.LastObservedCommitHash = revision
	if err := r.popFromRevisionQueueWithResult(ctx, mrt); err != nil {
		r.logger.Error(err, "Failed to update MSR spec in cluster after successful revision processed")
		return fmt.Errorf("update ManifestRequestTemplate status after revision %s processed: %w", revision, err)
	}

	r.logger.Info("Finished MSR process successfully")
	return nil
}

func (r *ManifestRequestTemplateReconciler) getMSRWithNewVersion(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, msr *governancev1alpha1.ManifestSigningRequest, revision string, changedFiles []governancev1alpha1.FileChange) *governancev1alpha1.ManifestSigningRequest {
	mrtSpecCpy := mrt.Spec.DeepCopy()
	updatedMSR := msr.DeepCopy()

	updatedMSR.Spec.Version = updatedMSR.Spec.Version + 1
	updatedMSR.Spec.MRT = governancev1alpha1.VersionedManifestRef{
		Name:      mrt.ObjectMeta.Name,
		Namespace: mrt.ObjectMeta.Namespace,
		Version:   mrt.Spec.Version,
	}
	updatedMSR.Spec.PublicKey = mrtSpecCpy.PGP.PublicKey
	updatedMSR.Spec.GitRepository = governancev1alpha1.GitRepository{
		SSHURL: mrt.Spec.GitRepository.SSHURL,
	}
	updatedMSR.Spec.Location = *mrtSpecCpy.Location.DeepCopy()
	updatedMSR.Spec.Changes = changedFiles
	updatedMSR.Spec.Governors = *mrtSpecCpy.Governors.DeepCopy()
	updatedMSR.Spec.Require = *mrtSpecCpy.Require.DeepCopy()
	updatedMSR.Spec.Status = governancev1alpha1.InProgress

	return updatedMSR
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

		// Skip files inside of governanceFolder or operational folder
		// TODO: we suppose, that MRT is created outside of governanceFolder. Otherwise, it will be skipped.
		// TODO: on creation check, that MSR is not created inside of governanceFolder. Or improve the logic
		isGovernanceFile := strings.HasPrefix(filePath, governanceFolder+"/")
		isQubmangoOperationalFile := strings.HasPrefix(filePath, QubmangoOperationalFolder+"/")

		if isGovernanceFile || isQubmangoOperationalFile {
			continue
		}

		// Add the remaining file (after both checks) to the list
		filtered = append(filtered, file)
	}

	return filtered
}

func (r *ManifestRequestTemplateReconciler) popFromRevisionQueueWithResult(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) error {
	if len(mrt.Status.RevisionsQueue) == 0 {
		return nil
	}

	mrt.Status.RevisionsQueue = mrt.Status.RevisionsQueue[1:]
	if err := r.Status().Update(ctx, mrt); err != nil {
		r.logger.Error(err, "Failed to update ManifestRequestTemplate status")
		return fmt.Errorf("update ManifestRequestTemplate status: %w", err)
	}

	return nil
}

// getApplication fetches the ArgoCD Application resource referenced by the ManifestRequestTemplate
func (r *ManifestRequestTemplateReconciler) getApplication(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate) (*argocdv1alpha1.Application, error) {
	app := &argocdv1alpha1.Application{}
	appKey := types.NamespacedName{
		Name:      mrt.Spec.ArgoCDApplication.Name,
		Namespace: mrt.Spec.ArgoCDApplication.Namespace,
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
