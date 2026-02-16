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

	argoappv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/go-logr/logr"
	unstructuredv1 "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	governancecontroller "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/controller"
)

const (
	ArgoCDTrackIDAnnotation = "argocd.argoproj.io/tracking-id"
)

// +kubebuilder:webhook:path=/mca/validate/argocd-requests,mutating=false,failurePolicy=fail,sideEffects=None,groups=*,resources=*,verbs=create;update;delete,versions=*,name=argocd-change-admission-webhook.governance.nazar.grynko.com,admissionReviewVersions=v1

func NewArgoCDChangeAdmissionValidator(
	client client.Client,
	decoder admission.Decoder,
) *ArgoCDChangeAdmissionValidator {
	return &ArgoCDChangeAdmissionValidator{
		Client:       client,
		Decoder:      decoder,
		initRevision: make(map[types.NamespacedName]string),
	}
}

type ArgoCDChangeAdmissionValidator struct {
	Client       client.Client
	Decoder      admission.Decoder
	initRevision map[types.NamespacedName]string
	logger       logr.Logger
}

// Handle is the function that gets called for validation admission request.
func (v *ArgoCDChangeAdmissionValidator) Handle(
	ctx context.Context,
	req admission.Request,
) admission.Response {
	// if true {
	// 	return admission.Allowed("Change approved by Manifest Change Approval")
	// }

	v.logger = logf.FromContext(ctx).WithValues("controller", "AdmissionWebhook", "user", req.UserInfo)

	// Extract the Application resource
	application, resp := v.getApplication(ctx, req)
	if resp != nil {
		return *resp
	}
	v.logger.WithValues("appName", application.Name, "appNamespace", application.Namespace)

	// Find the governing MRT
	mrt, resp := v.validateAndGetMRT(ctx, application)
	if resp != nil {
		return *resp
	}

	// Check if MRT is initialized. Allow the initialization revision on MRT initialization to avoid sync failures.
	revision := GetRevisionFromApplication(application)
	v.logger.WithValues("mrtName", mrt.Name, "mrtNamespace", mrt.Namespace, "requestedRevision", revision)

	if resp := v.validateMRTInitialization(mrt, revision); resp != nil {
		return *resp
	}

	// Validate the revision is approved
	return v.validateRevisionApproval(ctx, mrt, revision)
}

// validateAndGetMRT retrieves the MRT governing the Application
func (v *ArgoCDChangeAdmissionValidator) validateAndGetMRT(
	ctx context.Context,
	application *argoappv1.Application,
) (*governancev1alpha1.ManifestRequestTemplate, *admission.Response) {
	mrt, resp := v.getMRTForApplication(ctx, application)
	if resp != nil {
		return nil, resp
	}

	if mrt == nil {
		v.logger.V(2).Info("No governing MRT found for Application")
		resp := admission.Allowed("No governing MRT found, allow by default")
		return nil, &resp
	}

	return mrt, nil
}

// validateMRTInitialization checks if the MRT has been initialized
func (v *ArgoCDChangeAdmissionValidator) validateMRTInitialization(
	mrt *governancev1alpha1.ManifestRequestTemplate,
	revision string,
) *admission.Response {
	mrtKey := types.NamespacedName{Name: mrt.Name, Namespace: mrt.Namespace}
	initRevision, exist := v.initRevision[mrtKey]

	// MRT not yet initialized (no finalizer)
	if !controllerutil.ContainsFinalizer(mrt, governancecontroller.GovernanceFinalizer) {
		// Revision doesn't correspond to the initialization revision (newer revision)
		if exist && initRevision != revision {
			v.logger.V(2).Info("Governing MRT not initialized yet, wait")
			resp := admission.Denied("Governing MRT not initialized yet, deny by default")
			return &resp
		}

		// Initialization revision. Allow, to avoid infinite error loop
		if !exist || initRevision == revision {
			v.logger.V(2).Info("Governance initialization revision, allow")
			resp := admission.Allowed("Governance initialization revision, allow by default")
			return &resp
		}
	}

	delete(v.initRevision, mrtKey)
	return nil
}

// validateRevisionApproval checks if the revision has been approved by MCA
func (v *ArgoCDChangeAdmissionValidator) validateRevisionApproval(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	revision string,
) admission.Response {
	// Get the latest MCA for this MRT
	mca, resp := v.getMCAForMRT(ctx, mrt)
	if resp != nil {
		return *resp
	}

	// No MCA found - deny by default
	if mca == nil {
		v.logger.V(2).Info("No MCA found for MRT")
		return admission.Denied("No MCA found for MRT, deny by default")
	}
	v.logger.WithValues("mcaName", mca.Name, "mcaNamespace", mca.Namespace)

	// Check if revision is in approval history
	return v.checkRevisionApprovalStatus(revision, mca)
}

// checkRevisionApprovalStatus validates if the requested revision is approved
func (v *ArgoCDChangeAdmissionValidator) checkRevisionApprovalStatus(
	revision string,
	mca *governancev1alpha1.ManifestChangeApproval,
) admission.Response {
	// Find the revision in approval history
	requestedMCAIdx := slices.IndexFunc(mca.Status.ApprovalHistory, func(rec governancev1alpha1.ManifestChangeApprovalHistoryRecord) bool {
		return rec.CommitSHA == revision
	})

	// Revision not found in approval history
	if requestedMCAIdx == -1 {
		v.logger.V(3).Info("No approval found in MCA for requested revision")
		return admission.Denied(fmt.Sprintf("Change from commit %s has not been approved by the governance board.", revision))
	}

	// Check if it's the latest approved revision
	mcaRecord := mca.Status.ApprovalHistory[requestedMCAIdx]
	if mcaRecord.CommitSHA != mca.Spec.CommitSHA {
		v.logger.Info("Application revision doesn't equal the last approved commit in MCA", "lastApprovedCommitSHA", mca.Spec.CommitSHA)
		return admission.Denied(fmt.Sprintf("Change from commit %s doesn't equal the last approved commit %s in MCA.", revision, mca.Spec.CommitSHA))
	}

	// Revision is approved and latest - allow it
	v.logger.V(2).Info("Application request is approved by the latest MCA. Allow", "lastApprovedCommitSHA", mca.Spec.CommitSHA)
	return admission.Allowed("Change approved by Manifest Change Approval")
}

func (v *ArgoCDChangeAdmissionValidator) getApplication(
	ctx context.Context,
	req admission.Request,
) (*argoappv1.Application, *admission.Response) {
	if req.Kind.Kind != "Application" {
		return v.getApplicationFromNonApplicationRequest(ctx, req)
	} else {
		return v.getApplicationFromApplicationRequest(req)
	}
}

func (v *ArgoCDChangeAdmissionValidator) getApplicationFromNonApplicationRequest(
	ctx context.Context,
	req admission.Request,
) (*argoappv1.Application, *admission.Response) {
	gvk := v.getGroupVersionKind(req)

	unstruct := &unstructuredv1.Unstructured{}
	unstruct.SetGroupVersionKind(gvk)
	if err := v.Decoder.Decode(req, unstruct); err != nil {
		v.logger.Error(err, "Failed decoding resource from ArgoCD request", "resource", gvk.Kind, "name", req.Name, "namespace", req.Namespace)
		resp := admission.Errored(http.StatusBadRequest, err)
		return nil, &resp
	}

	trackingID := unstruct.GetAnnotations()[ArgoCDTrackIDAnnotation]
	if trackingID == "" {
		v.logger.Info("ArgoCD tracking-id not found; denying by default to avoid unapproved applies", "resource", gvk.Kind, "name", req.Name, "namespace", req.Namespace)
		resp := admission.Denied("ArgoCD tracking-id not found; unable to verify approval for this resource")
		return nil, &resp
	}

	appName, appNamespace, ok := parseTrackingID(trackingID)
	if !ok {
		v.logger.Info("Failed to parse ArgoCD tracking-id; cannot map to Application", "trackingID", trackingID)
		resp := admission.Errored(http.StatusBadRequest, fmt.Errorf("invalid tracking-id format: %s", trackingID))
		return nil, &resp
	}

	// Fetch the ArgoCD Application
	appKey := types.NamespacedName{Name: appName, Namespace: appNamespace}
	application := &argoappv1.Application{}
	if err := v.Client.Get(ctx, appKey, application); err != nil {
		v.logger.Error(err, "Failed to fetch ArgoCD Application for tracking-id", "name", appName, "namespace", appNamespace)
		resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("get Application %s:%s: %w", appNamespace, appName, err))
		return nil, &resp
	}

	return application, nil
}

func (v *ArgoCDChangeAdmissionValidator) getApplicationFromApplicationRequest(
	req admission.Request,
) (*argoappv1.Application, *admission.Response) {
	gvk := v.getGroupVersionKind(req)

	application := &argoappv1.Application{}
	application.SetGroupVersionKind(gvk)
	if err := v.Decoder.Decode(req, application); err != nil {
		v.logger.Error(err, "Failed decoding resource from ArgoCD request", "resource", gvk.Kind, "name", req.Name, "namespace", req.Namespace)
		resp := admission.Errored(http.StatusBadRequest, err)
		return nil, &resp
	}

	return application, nil
}

func (v *ArgoCDChangeAdmissionValidator) getMRTForApplication(
	ctx context.Context,
	applicationObj *argoappv1.Application,
) (*governancev1alpha1.ManifestRequestTemplate, *admission.Response) {
	// Fetch list of all MRTs
	mrtList := &governancev1alpha1.ManifestRequestTemplateList{}
	if err := v.Client.List(ctx, mrtList); err != nil {
		v.logger.Error(err, "Failed to get ManifestRequestTemplates list while getting MRT")
		resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("list ManifestRequestTemplates: %w", err))
		return nil, &resp
	}

	for _, mrtItem := range mrtList.Items {
		// Check if this MRT governs this Application
		mrtName := mrtItem.Spec.ArgoCD.Application.Name
		mrtNamespace := mrtItem.Spec.ArgoCD.Application.Namespace
		if mrtName != applicationObj.Name || mrtNamespace != applicationObj.Namespace {
			continue
		}

		return &mrtItem, nil
	}

	// No MRT found
	return nil, nil
}

func (v *ArgoCDChangeAdmissionValidator) getMCAForMRT(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (*governancev1alpha1.ManifestChangeApproval, *admission.Response) {
	// Fetch list of all MRTs
	mcaList := &governancev1alpha1.ManifestChangeApprovalList{}
	if err := v.Client.List(ctx, mcaList); err != nil {
		v.logger.Error(err, "Failed to get ManifestChangeApproval list while getting MCA")
		resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("list ManifestChangeApproval: %w", err))
		return nil, &resp
	}

	// Find MCA for this revision
	for _, mcaItem := range mcaList.Items {
		if mcaItem.Name == mrt.Spec.MCA.Name && mcaItem.Namespace == mrt.Spec.MCA.Namespace {
			return &mcaItem, nil
		}
	}

	// No MCA found
	return nil, nil
}

func GetRevisionFromApplication(
	applicationObj *argoappv1.Application,
) string {
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

func (v *ArgoCDChangeAdmissionValidator) getGroupVersionKind(
	req admission.Request,
) schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   req.Kind.Group,
		Version: req.Kind.Version,
		Kind:    req.Kind.Kind,
	}
}

// InjectDecoder injects the decoder.
func (v *ArgoCDChangeAdmissionValidator) InjectDecoder(
	d admission.Decoder,
) error {
	v.Decoder = d
	return nil
}

// Helper: parse tracking-id into appName and appNamespace
func parseTrackingID(
	trackingID string,
) (string, string, bool) {
	// Expected: "<application-name>:<group>/<kind>:<resource-namespace>/<resource-name>"
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
