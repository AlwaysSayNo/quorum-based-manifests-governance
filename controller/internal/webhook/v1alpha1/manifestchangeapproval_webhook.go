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

package v1alpha1

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/controller/api/v1alpha1"
	"github.com/go-logr/logr"
	unstructuredv1 "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	argoappv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// nolint:unused
// log is for logging in this package.
var manifestchangeapprovallog = logf.Log.WithName("manifestchangeapproval-resource")

// +kubebuilder:webhook:path=/validate-all,mutating=false,failurePolicy=fail,sideEffects=None,groups=*,resources=*,verbs=create;update;delete,versions=*,name=mca-webhook.governance.nazar.grynko.com,admissionReviewVersions=v1,timeoutSeconds=30

type ManifestChangeApprovalCustomValidator struct {
	Client  client.Client
	Decoder admission.Decoder
}

// Handle is the function that gets called for validation admission request.
func (v *ManifestChangeApprovalCustomValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := logf.FromContext(ctx).WithValues("user", req.UserInfo.Username)

	// Get Application resource for request
	application, resp, ok := v.getApplication(ctx, &logger, req)
	if !ok {
		return *resp
	}

	// Get ManifestRequestTemplate resource linked to the current Application
	// TODO: what should we do, if Application doesn't have MRT bound to it? Maybe allow?
	mrt, resp, ok := v.getMRTForApplication(ctx, &logger, application)
	if !ok {
		return *resp
	} else if mrt == nil {
		logger.Info("No governing MRT found for Application", "application", application.Name, "namespace", application.Namespace)
		return admission.Denied("No governing MRT found, deny by default")
	}

	// Take revision commit hash from Application
	revision := v.getRevisionFromApplication(application)

	// Get the latest ManifestChangeApproval for current ManifestRequestTemplate
	mca, resp, ok := v.getMCAForMRT(ctx, &logger, mrt)
	if !ok {
		return *resp
	} else if mca == nil {
		logger.Info("No MCA found for MRT", "mrt", mrt.Name, "namespace", mrt.Namespace)

		// If there is no ManifestChangeApproval -- add the revision to ManifestRequestTemplate review queue
		if resp, ok := v.appendRevisionToMRTCommitQueue(ctx, &logger, &revision, mrt); !ok {
			return *resp
		}
		return admission.Denied("No MCA found for MRT, deny by default")
	}

	// Check, if current revision was approved before (for it exists ManifestChangeApproval)
	requestedMCAIdx := slices.IndexFunc(mca.Status.ApprovalHistory, func(rec governancev1alpha1.ManifestChangeApprovalHistoryRecord) bool {
		return rec.CommitSHA == revision
	})
	if requestedMCAIdx == -1 {
		logger.Info("No approval found in MCA for requested revision", "revision", revision, "mca", mca.Name)

		// If there is no ManifestChangeApproval, corresponding for this revision -- put it into ManifestRequestTemplate review queue
		if resp, ok := v.appendRevisionToMRTCommitQueue(ctx, &logger, &revision, mrt); !ok {
			return *resp
		}
		return admission.Denied(fmt.Sprintf("Change from commit %s has not been approved by the governance board.", revision))
	}

	// Check, if revision corresponds to the latest ManifestChangeApproval
	// If not, it might be rollback request and it should be handled differently
	mcaRecord := mca.Status.ApprovalHistory[requestedMCAIdx]
	if mcaRecord.CommitSHA != mca.Status.LastApprovedCommitSHA {
		logger.Info("Application revision is older than last approved commit in MCA", "requestedRevision", revision, "lastApprovedCommitSHA", mca.Status.LastApprovedCommitSHA)
		// TODO: might be rollback, and should be handled differently. Deny for now
		return admission.Denied(fmt.Sprintf("Change from commit %s is older than last approved commit %s in MCA.", revision, mca.Status.LastApprovedCommitSHA))
	}

	// Otherwise, the request is latest approved and we allow, to apply it
	logger.Info("Allowing Argo CD Application request approved by MCA", "application", application.Name, "namespace", application.Namespace, "commit", revision)
	return admission.Allowed("Change approved by Manifest Change Approval")
}

func (v *ManifestChangeApprovalCustomValidator) getApplication(ctx context.Context, logger *logr.Logger, req admission.Request) (*argoappv1.Application, *admission.Response, bool) {
	if req.Kind.Kind != "Application" {
		return v.getApplicationFromNonApplicationRequest(ctx, logger, req)
	} else {
		return v.getApplicationFromApplicationRequest(ctx, logger, req)
	}
}

func (v *ManifestChangeApprovalCustomValidator) getApplicationFromNonApplicationRequest(ctx context.Context, logger *logr.Logger, req admission.Request) (*argoappv1.Application, *admission.Response, bool) {
	gvk := v.getGroupVersionKind(req)

	unstruct := &unstructuredv1.Unstructured{}
	unstruct.SetGroupVersionKind(gvk)
	if err := v.Decoder.Decode(req, unstruct); err != nil {
		logger.Error(err, "Failed decoding resource from ArgoCD request", "resource", gvk.Kind, "name", req.Name, "namespace", req.Namespace)
		resp := admission.Errored(http.StatusBadRequest, err)
		return nil, &resp, false
	}

	trackingID := unstruct.GetAnnotations()["argocd.argoproj.io/tracking-id"]
	if trackingID == "" {
		logger.Info("ArgoCD tracking-id not found; denying by default to avoid unapproved applies", "resource", gvk.Kind, "name", req.Name, "namespace", req.Namespace)
		resp := admission.Denied("ArgoCD tracking-id not found; unable to verify approval for this resource")
		return nil, &resp, false
	}

	appName, appNamespace, ok := parseTrackingID(trackingID)
	if !ok {
		logger.Info("Failed to parse ArgoCD tracking-id; cannot map to Application", "trackingID", trackingID)
		resp := admission.Errored(http.StatusBadRequest, fmt.Errorf("invalid tracking-id format: %s", trackingID))
		return nil, &resp, false
	}

	// Fetch the ArgoCD Application
	application := &argoappv1.Application{}
	appKey := types.NamespacedName{Name: appName, Namespace: appNamespace}
	if err := v.Client.Get(ctx, appKey, application); err != nil {
		logger.Error(err, "Failed to fetch ArgoCD Application for tracking-id", "name", appName, "namespace", appNamespace)
		resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("get App %s.%s: %w", appNamespace, appName, err))
		return nil, &resp, false
	}

	return application, nil, true
}

func (v *ManifestChangeApprovalCustomValidator) getApplicationFromApplicationRequest(ctx context.Context, logger *logr.Logger, req admission.Request) (*argoappv1.Application, *admission.Response, bool) {
	gvk := v.getGroupVersionKind(req)

	application := &argoappv1.Application{}
	application.SetGroupVersionKind(gvk)
	if err := v.Decoder.Decode(req, application); err != nil {
		logger.Error(err, "Failed decoding resource from ArgoCD request", "resource", gvk.Kind, "name", req.Name, "namespace", req.Namespace)
		resp := admission.Errored(http.StatusBadRequest, err)
		return nil, &resp, false
	}

	return application, nil, true
}

func (v *ManifestChangeApprovalCustomValidator) getMRTForApplication(ctx context.Context, logger *logr.Logger, applicationObj *argoappv1.Application) (*governancev1alpha1.ManifestRequestTemplate, *admission.Response, bool) {
	// Fetch list of all MRTs
	mrtList := &governancev1alpha1.ManifestRequestTemplateList{}
	if err := v.Client.List(ctx, mrtList); err != nil {
		logger.Error(err, "Failed to get ManifestRequestTemplates list while getting MRT")
		resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("list ManifestRequestTemplates: %w", err))
		return nil, &resp, false
	}

	for _, mrtItem := range mrtList.Items {
		// Check if this MRT governs this Application
		mrtName := mrtItem.Spec.ArgoCDApplication.Name
		mrtNamespace := mrtItem.Spec.ArgoCDApplication.Namespace
		if mrtName != applicationObj.Name || mrtNamespace != applicationObj.Namespace {
			continue
		}

		return &mrtItem, nil, true
	}

	// No MRT found
	return nil, nil, true
}

func (v *ManifestChangeApprovalCustomValidator) getMCAForMRT(ctx context.Context, logger *logr.Logger, mrt *governancev1alpha1.ManifestRequestTemplate) (*governancev1alpha1.ManifestChangeApproval, *admission.Response, bool) {
	// Fetch list of all MRTs
	mcaList := &governancev1alpha1.ManifestChangeApprovalList{}
	if err := v.Client.List(ctx, mcaList); err != nil {
		logger.Error(err, "Failed to get ManifestChangeApproval list while getting MCA")
		resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("list ManifestChangeApproval: %w", err))
		return nil, &resp, false
	}

	// Find MCA for this revision
	for _, mcaItem := range mcaList.Items {
		if mcaItem.Name == mrt.Spec.MCA.Name && mcaItem.Namespace == mrt.Spec.MCA.Namespace {
			return &mcaItem, nil, true
		}
	}

	// No MCA found
	return nil, nil, true
}

func (v *ManifestChangeApprovalCustomValidator) getRevisionFromApplication(applicationObj *argoappv1.Application) string {
	// TODO: extra checks when do rollback or sync to previous revision
	revision := ""
	if applicationObj.Status.OperationState != nil && applicationObj.Status.OperationState.Operation.Sync != nil {
		// revision is stored in SyncResult (for ongoing or finished syncs)
		revision = applicationObj.Status.OperationState.Operation.Sync.Revision
	} else if applicationObj.Operation != nil && applicationObj.Operation.Sync != nil {
		// revision coming from argocd-server
		revision = applicationObj.Operation.Sync.Revision
	}

	return revision
}

func (v *ManifestChangeApprovalCustomValidator) appendRevisionToMRTCommitQueue(ctx context.Context, logger *logr.Logger, revision *string, mrt *governancev1alpha1.ManifestRequestTemplate) (*admission.Response, bool) {
	if mrt.Status.RevisionsQueue == nil {
		mrt.Status.RevisionsQueue = []string{}
	}

	if !slices.Contains(mrt.Status.RevisionsQueue, *revision) {
		mrt.Status.RevisionsQueue = append(mrt.Status.RevisionsQueue, *revision)
		if err := v.Client.Status().Update(ctx, mrt); err != nil { //TODO: or update object directly?
			logger.Error(err, "Failed to update MRT status with new commit in queue", "mrt", mrt.Name, "namespace", mrt.Namespace, "revision", revision)
			resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("update MRT %s.%s status with new commit in queue: %w", mrt.Namespace, mrt.Name, err))
			return &resp, false
		}
		logger.Info("Added revision to MRT commits queue for approval processing", "mrt", mrt.Name, "namespace", mrt.Namespace, "revision", revision)
	}

	return nil, true
}

// handleArgoCDRequest contains the MCA validation logic.
func (v *ManifestChangeApprovalCustomValidator) handleArgoCDRequest(ctx context.Context, req admission.Request) admission.Response {
	logger := logf.FromContext(ctx)

	gvk := v.getGroupVersionKind(req)
	if req.Kind.Kind == "Application" {
		applicationObj := &argoappv1.Application{}
		applicationObj.SetGroupVersionKind(gvk)
		if err := v.Decoder.Decode(req, applicationObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}

		if applicationObj.Operation == nil {
			logger.Info("Decoded with Argo CD Application object", "status", applicationObj.Status, "operation", applicationObj.Operation)
		} else {
			logger.Info("Decoded with Argo CD Application object", "status", applicationObj.Status, "operation", *applicationObj.Operation)
		}
	} else if req.Kind.Kind == "ConfigMap" {
		configmap := corev1.ConfigMap{}
		configmap.SetGroupVersionKind(gvk)
		if err := v.Decoder.Decode(req, &configmap); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		logger.Info("Decoded with Argo CD configmap object", "name", configmap)
	} else if req.Kind.Kind == "ManifestRequestTemplate" {
		mtrObj := governancev1alpha1.ManifestRequestTemplate{}
		mtrObj.SetGroupVersionKind(gvk)
		if err := v.Decoder.Decode(req, &mtrObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		logger.Info("Decoded with Argo CD MRT object", "name", mtrObj)
	} else {
		logger.Info("Decoded with Argo CD unknown request kind", "kind", gvk)
	}

	return admission.Allowed("Change approved by Manifest Change Approval")
}

func (v *ManifestChangeApprovalCustomValidator) getGroupVersionKind(req admission.Request) schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   req.Kind.Group,
		Version: req.Kind.Version,
		Kind:    req.Kind.Kind,
	}
}

// InjectDecoder injects the decoder.
func (v *ManifestChangeApprovalCustomValidator) InjectDecoder(d admission.Decoder) error {
	v.Decoder = d
	return nil
}

// Helper: parse tracking-id into appName and appNamespace
func parseTrackingID(trackingID string) (string, string, bool) {
	// Expected: "<application-name>:<group>/<kind>:<namespace>/<name>"
	parts := strings.SplitN(trackingID, ":", 3)
	if len(parts) < 3 {
		return "", "", false
	}
	appName := parts[0]
	// TODO: we suppose, that all Applications are stored inside the default for ArgoCD namespace -- argocd.
	// But ArgoCD has functionality since version 2.5, which allows to deploy Application resources in other namespace.
	// To handle this, extra logic is required.
	appNamespace := "argocd"
	return appName, appNamespace, true
}
